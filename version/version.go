// Package version 负责版本号常量、更新检查与版本比较。
package version

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Current 是程序版本号，构建时可通过
// -ldflags "-X gitee.com/liliaic/cfstx/version.Current=v1.0.0" 覆盖。
var Current = "v1.2.11"

// repoURL 是项目主页（主仓库）。
const repoURL = "https://gitee.com/liliaic/cfstx"

// RepoURL 对外暴露的项目主页，出现在 CLI 帮助和更新提示里。
//
// 用 Gitee 而不是 GitHub：面向的是国内用户，GitHub 大多数人打不开。
const RepoURL = repoURL

// RepoURLGitHub 是 GitHub 镜像仓库。
const RepoURLGitHub = "https://github.com/lilidd1216/cfstx"

// ReleasesURL 是 Release 列表页面。
const ReleasesURL = repoURL + "/releases"

// source 描述一个版本信息来源。
type source struct {
	Name       string // 展示名，出现在日志和错误信息里
	VersionURL string // 纯文本版本号，CLI -v 用
	ReleaseAPI string // Release 接口
	WebURL     string // 网页地址，用来兜底拼 Release 页面
	NeedsUA    bool   // GitHub API 不带 User-Agent 会返回 403
}

// sources 按顺序尝试。
//
// 顺序的选择：绝大多数用户在国内，Gitee 直连通常更快、更稳；
// 但 Gitee 本身有不可用风险（账号、审核、政策），所以 GitHub 作为兜底。
//
// 注意「没有新版」不算失败：Gitee 返回旧版本号时会继续问 GitHub，
// 这样两边的发布节奏不同步也不会漏掉更新。
var sources = []source{
	{
		Name:       "Gitee",
		VersionURL: "https://gitee.com/liliaic/cfstx/raw/master/version.txt",
		ReleaseAPI: "https://gitee.com/api/v5/repos/liliaic/cfstx/releases/latest",
		WebURL:     "https://gitee.com/liliaic/cfstx",
	},
	{
		Name:       "GitHub",
		VersionURL: "https://raw.githubusercontent.com/lilidd1216/cfstx/master/version.txt",
		ReleaseAPI: "https://api.github.com/repos/lilidd1216/cfstx/releases/latest",
		WebURL:     "https://github.com/lilidd1216/cfstx",
		NeedsUA:    true,
	},
}

// Asset 是 Release 中的一个附件。
//
// Gitee 和 GitHub 的这个字段名一致，可以共用。
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

// ReleaseInfo 是 Release 的摘要信息。
type ReleaseInfo struct {
	TagName string  `json:"tag_name"`
	Name    string  `json:"name"`
	Body    string  `json:"body"`
	HTMLURL string  `json:"html_url"`
	Assets  []Asset `json:"assets"`

	// Source 记录这条信息来自哪个平台（Gitee / GitHub）。
	// 调用方可以据此决定提示文案，或者判断是否需要换源重试。
	Source string `json:"-"`
}

// Check 读取版本号文件，返回比当前更新的版本号。
// 返回空字符串表示已是最新或所有来源都不可用。
func Check() string {
	for _, s := range sources {
		latest := fetchVersionFile(s.VersionURL)
		if latest != "" && IsNewer(Current, latest) {
			return latest
		}
	}
	return ""
}

// fetchVersionFile 取一个纯文本版本号，失败返回空。
func fetchVersionFile(u string) string {
	client := http.Client{Timeout: 10 * time.Second}
	res, err := client.Get(u)
	if err != nil {
		return ""
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return ""
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 256))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(body))
}

// CheckRelease 按 Gitee → GitHub 的顺序查询最新 Release。
//
// 仓库尚无 Release 时返回 (nil, nil)；所有来源都失败时返回最后一个错误。
func CheckRelease() (*ReleaseInfo, error) {
	var lastErr error
	for _, s := range sources {
		info, err := fetchRelease(s)
		if err != nil {
			lastErr = err
			continue
		}
		if info != nil {
			return info, nil
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, nil
}

// fetchRelease 从单个来源拉 Release。
func fetchRelease(s source) (*ReleaseInfo, error) {
	req, err := http.NewRequest(http.MethodGet, s.ReleaseAPI, nil)
	if err != nil {
		return nil, err
	}
	if s.NeedsUA {
		// GitHub 对没有 User-Agent 的请求直接返回 403
		req.Header.Set("User-Agent", "CFSTX")
		req.Header.Set("Accept", "application/vnd.github+json")
	}

	client := http.Client{Timeout: 10 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s api 返回 %s", s.Name, res.Status)
	}

	var info ReleaseInfo
	if err := json.NewDecoder(res.Body).Decode(&info); err != nil {
		return nil, err
	}
	info.Source = s.Name

	// Gitee 的 Release 接口不返回 html_url，自己拼一个。
	// 前端拿它做「前往下载」的兜底链接，空着就只能退到固定的 Releases 首页。
	if info.HTMLURL == "" && info.TagName != "" {
		info.HTMLURL = s.WebURL + "/releases/tag/" + info.TagName
	}
	return &info, nil
}

// IsNewer 判断 latest 是否比 current 更新（按 主.次.修订 比较）。
func IsNewer(current, latest string) bool {
	c := parseVersion(current)
	l := parseVersion(latest)
	for i := 0; i < 3; i++ {
		if l[i] > c[i] {
			return true
		}
		if l[i] < c[i] {
			return false
		}
	}
	return false
}

func parseVersion(v string) [3]int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	parts := strings.Split(v, ".")
	var out [3]int
	for i := 0; i < len(parts) && i < 3; i++ {
		n, _ := strconv.Atoi(parts[i])
		out[i] = n
	}
	return out
}
