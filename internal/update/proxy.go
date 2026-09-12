package update

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// ResolveProxy rewrites target through proxy. It supports a prefix proxy
// (https://ghfast.top/) and a {url} placeholder proxy.
func ResolveProxy(proxy, target string) (string, error) {
	if _, err := validateTargetURL(target); err != nil {
		return "", err
	}
	proxy = strings.TrimSpace(proxy)
	if proxy == "" {
		return target, nil
	}
	if err := ValidateProxy(proxy); err != nil {
		return "", err
	}
	if strings.Contains(proxy, "{url}") {
		return strings.ReplaceAll(proxy, "{url}", url.QueryEscape(target)), nil
	}
	return strings.TrimRight(proxy, "/") + "/" + target, nil
}

// ResolveRequestURL uses the configured proxy as the exclusive route. Direct
// access is selected only when no proxy is configured.
func ResolveRequestURL(proxy, target string) (string, error) {
	if strings.TrimSpace(proxy) == "" {
		return target, nil
	}
	return ResolveProxy(proxy, target)
}

// ValidateProxy checks that proxy is empty or a safe http(s) URL.
func ValidateProxy(proxy string) error {
	proxy = strings.TrimSpace(proxy)
	if proxy == "" {
		return nil
	}
	u, err := url.Parse(proxy)
	if err != nil {
		return fmt.Errorf("代理地址无效: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("代理地址仅支持 http/https")
	}
	if u.Host == "" {
		return fmt.Errorf("代理地址缺少主机名")
	}
	if u.User != nil {
		return fmt.Errorf("代理地址不能包含用户名或密码")
	}
	if u.Fragment != "" {
		return fmt.Errorf("代理地址不能包含片段")
	}
	if u.RawQuery != "" && !strings.Contains(proxy, "{url}") {
		return fmt.Errorf("带查询参数的代理地址必须使用 {url} 占位符")
	}
	if unsafeHost(u.Hostname()) {
		return fmt.Errorf("代理地址不能指向本机或私有网络")
	}
	return nil
}

func validateTargetURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("目标地址无效: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("目标地址仅支持 http/https")
	}
	if u.User != nil {
		return nil, fmt.Errorf("目标地址不能包含用户名或密码")
	}
	if u.Host == "" {
		return nil, fmt.Errorf("目标地址缺少主机名")
	}
	if unsafeHost(u.Hostname()) {
		return nil, fmt.Errorf("目标地址不能指向本机或私有网络")
	}
	return u, nil
}

func unsafeHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast())
}
