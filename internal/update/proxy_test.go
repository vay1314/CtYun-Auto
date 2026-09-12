package update

import "testing"

func TestResolveProxy(t *testing.T) {
	got, err := ResolveProxy("https://ghfast.top/", "https://github.com/x/y")
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://ghfast.top/https://github.com/x/y"; got != want {
		t.Fatalf("prefix proxy = %q, want %q", got, want)
	}

	got, err = ResolveProxy("https://proxy.example.com/?url={url}", "https://github.com/x/y")
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://proxy.example.com/?url=https%3A%2F%2Fgithub.com%2Fx%2Fy"; got != want {
		t.Fatalf("placeholder proxy = %q, want %q", got, want)
	}
}

func TestResolveProxyRejectsUnsafeTarget(t *testing.T) {
	if _, err := ResolveProxy("https://ghfast.top/", "ftp://github.com/x"); err == nil {
		t.Fatal("unsupported scheme was accepted")
	}
	if _, err := ResolveProxy("https://ghfast.top/", "https://user:pass@github.com/x"); err == nil {
		t.Fatal("target with credentials was accepted")
	}
	if _, err := ResolveProxy("http://127.0.0.1/", "https://github.com/x"); err == nil {
		t.Fatal("unsafe proxy was accepted")
	}
}

func TestResolveRequestURLUsesConfiguredProxyExclusively(t *testing.T) {
	target := "https://api.github.com/repos/example/project/releases/latest"
	got, err := ResolveRequestURL("https://proxy.example.com/", target)
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://proxy.example.com/" + target; got != want {
		t.Fatalf("configured proxy was not selected: got %q, want %q", got, want)
	}
	got, err = ResolveRequestURL("", target)
	if err != nil || got != target {
		t.Fatalf("direct route without proxy = %q, %v", got, err)
	}
}

func TestValidateProxy(t *testing.T) {
	if err := ValidateProxy(""); err != nil {
		t.Fatal(err)
	}
	if err := ValidateProxy("https://ghfast.top/"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateProxy("https://user:pass@proxy.example.com/"); err == nil {
		t.Fatal("proxy with credentials was accepted")
	}
	if err := ValidateProxy("ftp://proxy.example.com/"); err == nil {
		t.Fatal("proxy with unsupported scheme was accepted")
	}
	if err := ValidateProxy("https://proxy.example.com/?token=secret"); err == nil {
		t.Fatal("query proxy without URL placeholder was accepted")
	}
}
