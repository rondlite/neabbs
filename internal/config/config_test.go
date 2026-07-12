package config

import "testing"

func TestFromEnvWebDefaults(t *testing.T) {
	for _, k := range []string{"NEABBS_WEB", "NEABBS_WEB_DOMAIN", "NEABBS_CERTS"} {
		t.Setenv(k, "")
	}
	cfg := FromEnv()
	if cfg.WebListen != "" {
		t.Errorf("WebListen = %q, want empty (web off by default)", cfg.WebListen)
	}
	if cfg.WebDomain != "neabbs.com" {
		t.Errorf("WebDomain = %q, want neabbs.com", cfg.WebDomain)
	}
	if cfg.CertsDir != "./certs" {
		t.Errorf("CertsDir = %q, want ./certs", cfg.CertsDir)
	}
}

func TestFromEnvWebOverrides(t *testing.T) {
	t.Setenv("NEABBS_WEB", ":443")
	t.Setenv("NEABBS_WEB_DOMAIN", "example.org")
	t.Setenv("NEABBS_CERTS", "/data/certs")
	cfg := FromEnv()
	if cfg.WebListen != ":443" || cfg.WebDomain != "example.org" || cfg.CertsDir != "/data/certs" {
		t.Errorf("got %q %q %q", cfg.WebListen, cfg.WebDomain, cfg.CertsDir)
	}
}
