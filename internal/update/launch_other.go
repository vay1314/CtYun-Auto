//go:build !windows

package update

import "errors"

func LaunchUpdater(updaterPath, requestPath, token, installDir, dataDir string) error {
	return errors.New("当前平台不支持 Windows 更新助手")
}
