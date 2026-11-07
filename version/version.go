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
var Current = "v1.2.8"

const (
	// RepoURL 是项目主页。
	RepoURL = "https://gitee.com/liliaic/cfstx"

	// RemoteVersionURL 存放最新版本号的纯文本文件（CLI -v 使用）。
	RemoteVersionURL = "https://gitee.com/liliaic/cfstx/raw/master/version.txt"

	// ReleaseAPI 是 Gitee 最新 Release 接口。
	ReleaseAPI = "https://gitee.com/api/v5/repos/liliaic/cfstx/releases/latest"

	// ReleasesURL 是 Release 列表页面。
	ReleasesURL = RepoURL + "/releases"
)

// Asset 是 Release 中的一个附件。
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

// ReleaseInfo 是 Gitee Release 的摘要信息。
type ReleaseInfo struct {
	TagName string  `json:"tag_name"`
	Name    string  `json:"name"`
	Body    string  `json:"body"`
	HTMLURL string  `json:"html_url"`
	Assets  []Asset `json:"assets"`
}

// Check 读取 version.txt，返回比当前更新的版本号。
// 返回空字符串表示已是最新或检查失败。
func Check() string {
	client := http.Client{Timeout: 10 * time.Second}
	res, err := client.Get(RemoteVersionURL)
	if err != nil {
		return ""
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return ""
	}

	latest := strings.TrimSpace(string(body))
	if latest == "" || !IsNewer(Current, latest) {
		return ""
	}
	return latest
}

// CheckRelease 查询 Gitee 最新 Release。
// 仓库尚无 Release 时返回 (nil, nil)。
func CheckRelease() (*ReleaseInfo, error) {
	client := http.Client{Timeout: 10 * time.Second}
	res, err := client.Get(ReleaseAPI)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gitee api 返回 %s", res.Status)
	}

	var info ReleaseInfo
	if err := json.NewDecoder(res.Body).Decode(&info); err != nil {
		return nil, err
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
