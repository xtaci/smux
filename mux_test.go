// MIT License
//
// Copyright (c) 2016-2017 xtaci
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package smux

import (
	"bytes"
	"math"
	"testing"
)

type buffer struct {
	bytes.Buffer
}

func (b *buffer) Close() error {
	b.Buffer.Reset()
	return nil
}

func TestConfig(t *testing.T) {
	VerifyConfig(DefaultConfig())

	config := DefaultConfig()
	config.KeepAliveInterval = 0
	err := VerifyConfig(config)
	t.Log(err)
	if err == nil {
		t.Fatal("expected an error")
		return
	}

	config = DefaultConfig()
	config.KeepAliveInterval = 10
	config.KeepAliveTimeout = 5
	err = VerifyConfig(config)
	t.Log(err)
	if err == nil {
		t.Fatal("expected an error")
		return
	}

	config = DefaultConfig()
	config.MaxFrameSize = 0
	err = VerifyConfig(config)
	t.Log(err)
	if err == nil {
		t.Fatal("expected an error")
		return
	}

	config = DefaultConfig()
	config.MaxFrameSize = 65536
	err = VerifyConfig(config)
	t.Log(err)
	if err == nil {
		t.Fatal("expected an error")
		return
	}

	config = DefaultConfig()
	config.MaxReceiveBuffer = 0
	err = VerifyConfig(config)
	t.Log(err)
	if err == nil {
		t.Fatal("expected an error")
		return
	}

	config = DefaultConfig()
	config.MaxReceiveBuffer = math.MaxInt32 + 1
	err = VerifyConfig(config)
	t.Log(err)
	if err == nil {
		t.Fatal("expected an error")
		return
	}

	config = DefaultConfig()
	config.MaxStreamBuffer = 0
	err = VerifyConfig(config)
	t.Log(err)
	if err == nil {
		t.Fatal("expected an error")
		return
	}

	config = DefaultConfig()
	config.MaxStreamBuffer = 100
	config.MaxReceiveBuffer = 99
	err = VerifyConfig(config)
	t.Log(err)
	if err == nil {
		t.Fatal("expected an error")
		return
	}

	var bts buffer
	if _, err := Server(&bts, config); err == nil {
		t.Fatal("server started with wrong config")
		return
	}

	if _, err := Client(&bts, config); err == nil {
		t.Fatal("client started with wrong config")
		return
	}
}

func TestConfigMaxReceiveBufferUpperBound(t *testing.T) {
	config := DefaultConfig()
	config.MaxReceiveBuffer = math.MaxInt32 + 1
	if err := VerifyConfig(config); err == nil {
		t.Fatal("expected verify failure for excessive MaxReceiveBuffer")
	}

	var bts buffer
	if _, err := Server(&bts, config); err == nil {
		t.Fatal("server should reject excessive MaxReceiveBuffer")
	}
	if _, err := Client(&bts, config); err == nil {
		t.Fatal("client should reject excessive MaxReceiveBuffer")
	}
}
