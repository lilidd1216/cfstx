package task

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"gitee.com/liliaic/cfstx/utils"

	"github.com/VividCortex/ewma"
)

const (
	bufferSize = 1024
	// 原默认地址 https://cf.xiu2.xyz/url 已被 Cloudflare 封禁（403），
	// 改用 CF 官方测速端点：HEAD 请求返回 200（可用于延迟测速），
	// GET 返回大文件（可用于下载测速），且在所有 CF 标准端口上都可用。
	//
	// 注意：CF 对该端点单次请求有 100MB 上限（实测 bytes >= 100000000 返回 403），
	// 所以这里取 90MB，既接近上限又留出余量。
	defaultURL                     = "https://speed.cloudflare.com/__down?bytes=90000000"
	defaultTimeout                 = 10 * time.Second
	defaultDisableDownload         = false
	defaultTestNum                 = 10
	defaultMinSpeed        float64 = 0.0
	// 指定速度下限时，最多测「目标数量 × 该倍数」个 IP，防止测速时间失控
	maxDownloadCandidates = 10
)

var (
	URL     = defaultURL
	Timeout = defaultTimeout
	Disable = defaultDisableDownload

	TestCount = defaultTestNum
	MinSpeed  = defaultMinSpeed
)

// HttpingURL 返回延迟测速专用地址。
//
// speed.cloudflare.com 的 __down 端点按 bytes 参数计算配额，连 HEAD 请求也计入。
// 延迟测速要发数千次请求，若沿用下载测速的 90MB 地址，很快就会 429，
// 导致所有 IP 都测不通。所以这里强制换成 bytes=0。
func HttpingURL() string {
	if strings.Contains(URL, "speed.cloudflare.com/__down") {
		return "https://speed.cloudflare.com/__down?bytes=0"
	}
	return URL
}

func checkDownloadDefault() {
	if URL == "" {
		URL = defaultURL
	}
	if Timeout <= 0 {
		Timeout = defaultTimeout
	}
	if TestCount <= 0 {
		TestCount = defaultTestNum
	}
	if MinSpeed <= 0.0 {
		MinSpeed = defaultMinSpeed
	}
}

func TestDownloadSpeed(ipSet utils.PingDelaySet) (speedSet utils.DownloadSpeedSet) {
	checkDownloadDefault()
	if Disable {
		return utils.DownloadSpeedSet(ipSet)
	}
	if len(ipSet) <= 0 { // IP 数组长度(IP数量) 大于 0 时才会继续下载测速
		utils.Yellow.Println("[信息] 延迟测速结果 IP 数量为 0，跳过下载测速。")
		return
	}
	testNum := TestCount // 等待下载测速的队列数量 先默认等于 下载测速数量(-dn）
	if len(ipSet) < TestCount {
		testNum = len(ipSet)
	} else if MinSpeed > 0 {
		// 指定了下载速度下限时，需要多测一些才能凑够目标数量。
		// 但现在的 IP 库很容易筛出上千个可用 IP，若不加限制会连续测几个小时，
		// 所以最多测 TestCount*10 个。
		testNum = TestCount * maxDownloadCandidates
		if testNum > len(ipSet) {
			testNum = len(ipSet)
		}
	}
	if testNum < TestCount { // 如果 等待下载测速的队列数量 小于 下载测速数量(-dn），（显然 -dn 预期数量是不够的），所以 下载测速数量(-dn）修正为 等待下载测速的队列数量
		TestCount = testNum
	}

	utils.Cyan.Printf("开始下载测速（下限：%.2f MB/s, 数量：%d, 队列：%d）\n", MinSpeed, TestCount, testNum)
	// 控制 下载测速进度条 与 延迟测速进度条 长度一致（强迫症）
	bar_a := len(strconv.Itoa(len(ipSet)))
	bar_b := "     "
	for i := 0; i < bar_a; i++ {
		bar_b += " "
	}
	bar := utils.NewBar(TestCount, bar_b, "")
	for i := 0; i < testNum; i++ {
		if cancelled() {
			break
		}
		speed, colo := downloadHandler(&Target{IP: ipSet[i].IP, Port: ipSet[i].Port})
		ipSet[i].DownloadSpeed = speed
		if ipSet[i].Colo == "" { // 只有当 Colo 是空的时候，才写入，否则代表之前是 httping 测速并获取过了
			ipSet[i].Colo = colo
		}
		// 在每个 IP 下载测速后，以 [下载速度下限] 条件过滤结果
		if speed >= MinSpeed*1024*1024 {
			bar.Grow(1, "")
			speedSet = append(speedSet, ipSet[i]) // 高于下载速度下限时，添加到新数组中
			if ResultHook != nil {                // 界面实时追加，不用等全部测完
				ResultHook(ipSet[i])
			}
			if len(speedSet) == TestCount {       // 凑够满足条件的 IP 时（下载测速数量 -dn），就跳出循环
				emitProgress(ProgressEvent{
					Stage: "download", Current: i + 1, Total: testNum,
					IP: ipSet[i].IP.String(), Port: ipSet[i].Port, Speed: speed / 1024 / 1024, Colo: ipSet[i].Colo,
				})
				break
			}
		}
		emitProgress(ProgressEvent{
			Stage: "download", Current: i + 1, Total: testNum,
			IP: ipSet[i].IP.String(), Speed: speed / 1024 / 1024, Colo: ipSet[i].Colo,
		})
	}
	bar.Done()
	if MinSpeed == 0.00 { // 如果没有指定下载速度下限，则直接返回所有测速数据
		speedSet = utils.DownloadSpeedSet(ipSet)
	} else if len(speedSet) == 0 {
		// 测速跑过但没有任何 IP 达到下限：返回全部结果，
		// 否则界面会显示 0 个，让人误以为测速失败
		utils.Yellow.Printf("[提示] 没有 IP 达到 %.2f MB/s 的下载速度下限，返回全部测速结果供参考（可适当调低速度下限）。\n", MinSpeed)
		emitLog("warn", fmt.Sprintf("没有 IP 达到 %.2f MB/s 的速度下限，已返回全部 %d 个测速结果", MinSpeed, len(ipSet)))
		speedSet = utils.DownloadSpeedSet(ipSet)
	}
	// 按速度排序
	sort.Sort(speedSet)
	return
}

// getDialContext 把连接强制指向目标 IP:端口，每个目标用自己的端口。
func getDialContext(t *Target) func(ctx context.Context, network, address string) (net.Conn, error) {
	fakeSourceAddr := t.Addr()
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, fakeSourceAddr)
	}
}

// 统一的请求报错调试输出
func printDownloadDebugInfo(t *Target, err error, statusCode int, url, lastRedirectURL string, response *http.Response) {
	finalURL := url // 默认的最终 URL，这样当 response 为空时也能输出
	if lastRedirectURL != "" {
		finalURL = lastRedirectURL // 如果 lastRedirectURL 不是空，说明重定向过，优先输出最后一次要重定向至的目标
	} else if response != nil && response.Request != nil && response.Request.URL != nil {
		finalURL = response.Request.URL.String() // 如果 response 不为 nil，且 Request 和 URL 都不为 nil，则获取最后一次成功的响应地址
	}
	if url != finalURL { // 如果 URL 和最终地址不一致，说明有重定向，是该重定向后的地址引起的错误
		if statusCode > 0 { // 如果状态码大于 0，说明是后续 HTTP 状态码引起的错误
			utils.Red.Printf("[调试] IP: %s, 下载测速终止，HTTP 状态码: %d, 下载测速地址: %s, 出错的重定向后地址: %s\n", t.Addr(), statusCode, url, finalURL)
		} else {
			utils.Red.Printf("[调试] IP: %s, 下载测速失败，错误信息: %v, 下载测速地址: %s, 出错的重定向后地址: %s\n", t.Addr(), err, url, finalURL)
		}
	} else { // 如果 URL 和最终地址一致，说明没有重定向
		if statusCode > 0 { // 如果状态码大于 0，说明是后续 HTTP 状态码引起的错误
			utils.Red.Printf("[调试] IP: %s, 下载测速终止，HTTP 状态码: %d, 下载测速地址: %s\n", t.Addr(), statusCode, url)
		} else {
			utils.Red.Printf("[调试] IP: %s, 下载测速失败，错误信息: %v, 下载测速地址: %s\n", t.Addr(), err, url)
		}
	}
}

// return download Speed
func downloadHandler(t *Target) (float64, string) {
	var lastRedirectURL string // 用于记录最后一次重定向目标，以便在访问错误时输出
	client := &http.Client{
		Transport: &http.Transport{DialContext: getDialContext(t)},
		Timeout:   Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			lastRedirectURL = req.URL.String() // 记录每次重定向的目标，以便在访问错误时输出
			if len(via) > 10 {                 // 限制最多重定向 10 次
				if utils.Debug { // 调试模式下，输出更多信息
					utils.Red.Printf("[调试] IP: %s, 下载测速地址重定向次数过多，终止测速，下载测速地址: %s\n", t.Addr(), req.URL.String())
				}
				return http.ErrUseLastResponse
			}
			if req.Header.Get("Referer") == defaultURL { // 当使用默认下载测速地址时，重定向不携带 Referer
				req.Header.Del("Referer")
			}
			return nil
		},
	}
	defer client.CloseIdleConnections()
	req, err := http.NewRequest("GET", URL, nil)
	if err != nil {
		if utils.Debug { // 调试模式下，输出更多信息
			utils.Red.Printf("[调试] IP: %s, 下载测速请求创建失败，错误信息: %v, 下载测速地址: %s\n", t.Addr(), err, URL)
		}
		return 0.0, ""
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_12_6) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/98.0.4758.80 Safari/537.36")

	response, err := client.Do(req)
	if err != nil {
		if utils.Debug { // 调试模式下，输出更多信息
			printDownloadDebugInfo(t, err, 0, URL, lastRedirectURL, response)
		}
		return 0.0, ""
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		if utils.Debug { // 调试模式下，输出更多信息
			printDownloadDebugInfo(t, nil, response.StatusCode, URL, lastRedirectURL, response)
		}
		return 0.0, ""
	}

	// 通过头部参数获取地区码
	colo := getHeaderColo(response.Header)

	timeStart := time.Now()           // 开始时间（当前）
	timeEnd := timeStart.Add(Timeout) // 加上下载测速时间得到的结束时间

	contentLength := response.ContentLength // 文件大小
	buffer := make([]byte, bufferSize)

	var (
		contentRead     int64 = 0
		timeSlice             = Timeout / 100
		timeCounter           = 1
		lastContentRead int64 = 0
	)

	var nextTime = timeStart.Add(timeSlice * time.Duration(timeCounter))
	e := ewma.NewMovingAverage()

	// 循环计算，如果文件下载完了（两者相等），则退出循环（终止测速）
	for contentLength != contentRead {
		currentTime := time.Now()
		if currentTime.After(nextTime) {
			timeCounter++
			nextTime = timeStart.Add(timeSlice * time.Duration(timeCounter))
			e.Add(float64(contentRead - lastContentRead))
			lastContentRead = contentRead
		}
		// 如果超出下载测速时间，则退出循环（终止测速）
		if currentTime.After(timeEnd) {
			break
		}
		bufferRead, err := response.Body.Read(buffer)
		if err != nil {
			if err != io.EOF { // 如果文件下载过程中遇到报错（如 Timeout），且并不是因为文件下载完了，则退出循环（终止测速）
				break
			} else if contentLength == -1 { // 文件下载完成 且 文件大小未知，则退出循环（终止测速），例如：https://speed.cloudflare.com/__down?bytes=200000000 这样的，如果在 10 秒内就下载完成了，会导致测速结果明显偏低甚至显示为 0.00（下载速度太快时）
				break
			}
			// 获取上个时间片
			last_time_slice := timeStart.Add(timeSlice * time.Duration(timeCounter-1))
			// 下载数据量 / (用当前时间 - 上个时间片/ 时间片)
			e.Add(float64(contentRead-lastContentRead) / (float64(currentTime.Sub(last_time_slice)) / float64(timeSlice)))
		}
		contentRead += int64(bufferRead)
	}
	// 用「实际下载字节数 / 实际耗时」计算速度。
	// 原实现按固定的测速时长归一化，当文件在超时前就下载完时会明显低估速度。
	elapsed := time.Since(timeStart)
	if elapsed < time.Millisecond {
		elapsed = time.Millisecond
	}
	return float64(contentRead) / elapsed.Seconds(), colo
}
