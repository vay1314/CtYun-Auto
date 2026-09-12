package update

import "testing"

func TestParseManifest(t *testing.T) {
	raw := `{
	  "schemaVersion": 1,
	  "version": "2.1.0",
	  "tag": "v2.1.0",
	  "assets": {
	    "windows-amd64": {"name": "CtYunKeeper-v2.1.0-windows-amd64.zip", "sha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "packageManifestSha256": "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", "size": 10},
	    "linux-arm64": {"name": "CtYunKeeper-v2.1.0-linux-arm64.tar.gz", "sha256": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "packageManifestSha256": "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd", "size": 10}
	  }
	}`
	manifest, err := ParseManifest([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := manifest.AssetFor("windows", "amd64"); !ok {
		t.Fatal("expected windows-amd64 asset")
	}
	if _, ok := manifest.AssetFor("linux", "amd64"); ok {
		t.Fatal("linux-amd64 should not be present")
	}
}

func TestParseManifestRejectsMismatchedTag(t *testing.T) {
	raw := `{"schemaVersion":1,"version":"2.1.0","tag":"v2.2.0","assets":{"linux-amd64":{"name":"x.tar.gz","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","size":1}}}`
	if _, err := ParseManifest([]byte(raw)); err == nil {
		t.Fatal("mismatched tag was accepted")
	}
}

func TestParseManifestRejectsBadSHA(t *testing.T) {
	raw := `{"schemaVersion":1,"version":"2.1.0","tag":"v2.1.0","assets":{"linux-amd64":{"name":"x.tar.gz","sha256":"short","size":1}}}`
	if _, err := ParseManifest([]byte(raw)); err == nil {
		t.Fatal("invalid sha256 was accepted")
	}
}

func TestParseManifestRejectsMissingPackageManifestSHA(t *testing.T) {
	raw := `{"schemaVersion":1,"version":"2.1.0","tag":"v2.1.0","assets":{"linux-amd64":{"name":"x.tar.gz","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","size":1}}}`
	if _, err := ParseManifest([]byte(raw)); err == nil {
		t.Fatal("manifest without package manifest digest was accepted")
	}
}

func TestParseManifestRejectsUnsafeReleaseURL(t *testing.T) {
	raw := []byte(`{"schemaVersion":1,"version":"2.1.0","tag":"v2.1.0","releaseUrl":"javascript:alert(1)","assets":{"windows-amd64":{"name":"app.zip","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","packageManifestSha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","size":1}}}`)
	if _, err := ParseManifest(raw); err == nil {
		t.Fatal("unsafe release URL was accepted")
	}
}

func TestCompatibleWithAllowsDevelopmentBuildToEnterFormalChannel(t *testing.T) {
	manifest := Manifest{DatabaseVersion: DatabaseVersion, MinimumAppVersion: "9.0.0"}
	if err := manifest.CompatibleWith("dev-abc123", Platform{OS: "windows", Arch: "amd64"}); err != nil {
		t.Fatalf("development build was blocked by the formal minimum app version: %v", err)
	}
}
