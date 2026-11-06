// Package version 负责版本号常量与更新检查。
package version

import (
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	// Current 是程序默认版本号，构建时可通过
	// -ldflags "-X gitee.com/liliaic/cfstx/version.Current=v1.0.0" 覆盖。
	Current = "v1.0.0"

	// RepoURL 是项目主页。
	RepoURL = "https://gitee.com/liliaic/cfstx"

	// RemoteVersionURL 存放最新版本号的纯文本文件。
	RemoteVersionURL = "https://gitee.com/liliaic/cfstx/raw/master/version.txt"
)

// Check 拉取远端版本号。返回空字符串表示当前已是最新版或检查失败。
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
	if latest == "" || latest == Current {
		return ""
	}
	return latest
}
