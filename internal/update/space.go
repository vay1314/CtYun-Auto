package update

import (
	"fmt"
	"os"
	"path/filepath"
)

func CheckInstallSpace(dataDir, installDir string, expanded int64) error {
	var application, database int64
	for _, name := range []string{"ctyun-keeper", "ctyun-keeper.exe", "ctyun-keeper-updater.exe", "static", "README.txt"} {
		err := filepath.Walk(filepath.Join(installDir, name), func(_ string, info os.FileInfo, err error) error {
			if os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				return err
			}
			if info.Mode().IsRegular() {
				application += info.Size()
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if info, err := os.Stat(filepath.Join(dataDir, "ctyun-keeper.db") + suffix); err == nil {
			database += info.Size()
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	required := uint64(expanded*2 + application*2 + database*2 + (32 << 20))
	free, err := AvailableDiskSpace(dataDir)
	if err != nil {
		return err
	}
	if free < required {
		return fmt.Errorf("数据目录空间不足：更新、程序及数据库备份需要 %.1f MB", float64(required)/(1<<20))
	}
	free, err = AvailableDiskSpace(installDir)
	if err != nil {
		return err
	}
	if free < uint64(expanded*2+application) {
		return fmt.Errorf("安装目录空间不足")
	}
	return nil
}
