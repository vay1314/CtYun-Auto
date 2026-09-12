package update

import (
	"os"
	"runtime"
	"strings"
)

type Platform struct {
	OS              string
	Arch            string
	InDocker        bool
	LauncherVersion string
	BuiltinVersion  string
	ImageVersion    string
	RuntimeVersion  string
}

func CurrentPlatform() Platform {
	return Platform{
		OS:              runtime.GOOS,
		Arch:            runtime.GOARCH,
		InDocker:        inDocker(),
		LauncherVersion: strings.TrimSpace(os.Getenv("CTYUN_LAUNCHER_VERSION")),
		BuiltinVersion:  strings.TrimSpace(os.Getenv("CTYUN_BUILTIN_VERSION")),
		ImageVersion:    strings.TrimSpace(os.Getenv("CTYUN_IMAGE_VERSION")),
		RuntimeVersion:  strings.TrimSpace(os.Getenv("CTYUN_RUNTIME_VERSION")),
	}
}

func (p Platform) AssetKey() string {
	return p.OS + "-" + p.Arch
}

func inDocker() bool {
	if os.Getenv("CTYUN_CONTAINER") == "true" {
		return true
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	if data, err := os.ReadFile("/proc/1/cgroup"); err == nil {
		return strings.Contains(string(data), "docker")
	}
	return false
}
