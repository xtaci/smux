<img src="assets/smux.png" alt="smux" height="35px" />

[![GoDoc][1]][2] [![MIT licensed][3]][4] [![Build Status][5]][6] [![Go Report Card][7]][8] [![Coverage Statusd][9]][10] [![Sourcegraph][11]][12]

<img src="assets/mux.jpg" alt="smux" height="120px" /> 

[1]: https://godoc.org/github.com/xtaci/smux?status.svg
[2]: https://godoc.org/github.com/xtaci/smux
[3]: https://img.shields.io/badge/license-MIT-blue.svg
[4]: LICENSE
[5]: https://img.shields.io/github/created-at/xtaci/smux
[6]: https://img.shields.io/github/created-at/xtaci/smux
[7]: https://goreportcard.com/badge/github.com/xtaci/smux
[8]: https://goreportcard.com/report/github.com/xtaci/smux
[9]: https://codecov.io/gh/xtaci/smux/branch/master/graph/badge.svg
[10]: https://codecov.io/gh/xtaci/smux
[11]: https://sourcegraph.com/github.com/xtaci/smux/-/badge.svg
[12]: https://sourcegraph.com/github.com/xtaci/smux?badge

[English](README.md) | [中文](README_zh-cn.md)

## 简介

Smux (**S**imple **MU**ltiple**X**ing) 是一个 Golang 的多路复用库。它依赖于底层的连接（如 TCP 或 [KCP](https://github.com/xtaci/kcp-go)）来提供可靠性和顺序保证，并提供面向流的多路复用功能。该库最初是为 [kcp-go](https://github.com/xtaci/kcp-go) 的连接管理而设计的。

## 特性

1. ***令牌桶*** 控制接收，提供更平滑的带宽曲线（见下图）。
2. 会话级（Session-wide）接收缓冲区在流之间共享，**完全控制**整体内存使用。
3. 最小化头部（8 字节），最大化有效载荷。
4. 在 [kcptun](https://github.com/xtaci/kcptun) 中经过数百万设备的实战考验。
5. 内置公平队列流量整形。
6. 流滑动窗口，用于每条流的拥塞控制（协议版本 2+）。

![smooth bandwidth curve](assets/curve.jpg)

## 架构

* **Session**: 多路复用连接的主要管理器。它管理底层的 `io.ReadWriteCloser`，处理流的创建/接受，并管理共享的接收缓冲区。
* **Stream**: 会话中的逻辑流。它实现了 `net.Conn` 接口，处理数据缓冲和流控制。
* **Frame**: 数据传输的线上传输格式。

## 文档

有关完整文档，请参阅相关的 [Godoc](https://godoc.org/github.com/xtaci/smux)。

## 基准测试 (Benchmark)
```
$ go test -v -run=^$ -bench .
goos: darwin
goarch: amd64
pkg: github.com/xtaci/smux
BenchmarkMSB-4           	30000000	        51.8 ns/op
BenchmarkAcceptClose-4   	   50000	     36783 ns/op
BenchmarkConnSmux-4      	   30000	     58335 ns/op	2246.88 MB/s	    1208 B/op	      19 allocs/op
BenchmarkConnTCP-4       	   50000	     25579 ns/op	5124.04 MB/s	       0 B/op	       0 allocs/op
PASS
ok  	github.com/xtaci/smux	7.811s
```

## 规范 (Specification)

```
VERSION(1B) | CMD(1B) | LENGTH(2B) | STREAMID(4B) | DATA(LENGTH)  

VALUES FOR LATEST VERSION:
VERSION:
    1/2
    
CMD:
    cmdSYN(0)
    cmdFIN(1)
    cmdPSH(2)
    cmdNOP(3)
    cmdUPD(4)	// 仅在版本 2 中支持
    
STREAMID:
    客户端使用从 1 开始的奇数
    服务端使用从 0 开始的偶数
    
cmdUPD:
    | CONSUMED(4B) | WINDOW(4B) |
```

## 用法 (Usage)

```go

func client() {
    // 获取一个 TCP 连接
    conn, err := net.Dial(...)
    if err != nil {
        panic(err)
    }

    // 设置 smux 的客户端
    session, err := smux.Client(conn, nil)
    if err != nil {
        panic(err)
    }

    // 打开一个新的流
    stream, err := session.OpenStream()
    if err != nil {
        panic(err)
    }

    // Stream 实现了 io.ReadWriteCloser 接口
    stream.Write([]byte("ping"))
    stream.Close()
    session.Close()
}

func server() {
    // 接受一个 TCP 连接
    conn, err := listener.Accept()
    if err != nil {
        panic(err)
    }

    // 设置 smux 的服务端
    session, err := smux.Server(conn, nil)
    if err != nil {
        panic(err)
    }

    // 接受一个流
    stream, err := session.AcceptStream()
    if err != nil {
        panic(err)
    }

    // 监听消息
    buf := make([]byte, 4)
    stream.Read(buf)
    stream.Close()
    session.Close()
}

```

## 配置

`smux.Config` 允许调整会话参数：

* `Version`: 协议版本（1 或 2）。
* `KeepAliveInterval`: 发送 NOP 帧以保持连接存活的间隔。
* `KeepAliveTimeout`: 如果未接收到数据，关闭会话的超时时间。
* `MaxFrameSize`: 帧的最大大小。
* `MaxReceiveBuffer`: 共享接收缓冲区的最大大小。
* `MaxStreamBuffer`: 每个流缓冲区的最大大小。
