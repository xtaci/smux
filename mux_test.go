package smux

import "testing"

func TestConfig(t *testing.T) {
	VerifyConfig(DefaultConfig())

	config := DefaultConfig()
	config.KeepAliveInterval = 0
	err := VerifyConfig(config)
	t.Log(err)
	if err == nil {
		t.Fatal(err)
	}

	config = DefaultConfig()
	config.KeepAliveInterval = 10
	config.KeepAliveTimeout = 5
	err = VerifyConfig(config)
	t.Log(err)
	if err == nil {
		t.Fatal(err)
	}

	config = DefaultConfig()
	config.MaxFrameSize = 0
	err = VerifyConfig(config)
	t.Log(err)
	if err == nil {
		t.Fatal(err)
	}

	config = DefaultConfig()
	config.MaxFrameTokens = 0
	err = VerifyConfig(config)
	t.Log(err)
	if err == nil {
		t.Fatal(err)
	}
}
