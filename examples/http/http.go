// Copyright 2019 Andy Pan. All rights reserved.
// Copyright 2017 Joshua J Baker. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// 已测试运行，在服务器上面
// go mod init .
// go run http.go
// 2020/05/03 17:06:09 HTTP server is listening on [::]:8080 (multi-cores: true, loops: 1)

// 另起一个窗口
// curl 127.0.0.1:8080
// Hello World!

// 就是使用gnet，自己启动了一个http服务，并定义返回值，没有用官方的net/http

package main

import (
	"flag"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"github.com/panjf2000/gnet"
)

// 响应结果
var res string

// 自定义请求结构体，使用gnet的目的
type request struct {
	proto, method string
	path, query   string
	head, body    string
	remoteAddr    string
}

// 创建一个gnet服务
type httpServer struct {
	*gnet.EventServer
}

// 错误信息
var errMsg = "Internal Server Error"
var errMsgBytes = []byte(errMsg)

// http相关结构体
type httpCodec struct {
	req request
}

// httpCodec 完善数据流
func (hc *httpCodec) Encode(c gnet.Conn, buf []byte) (out []byte, err error) {
	if c.Context() == nil {
		return buf, nil
	}
	return appendResp(out, "500 Error", "", errMsg+"\n"), nil
}

// httpCodec 解析数据流
func (hc *httpCodec) Decode(c gnet.Conn) (out []byte, err error) {
	buf := c.Read()
	c.ResetBuffer()

	// process the pipeline
	var leftover []byte
pipeline: // 开启管道功能
	leftover, err = parseReq(buf, &hc.req)
	// bad thing happened
	if err != nil {
		c.SetContext(err)
		return nil, err
	} else if len(leftover) == len(buf) {
		// request not ready, yet
		return
	}
	out = appendHandle(out, res)
	buf = leftover
	goto pipeline
}

// httpSever 打印服务信息方法
func (hs *httpServer) OnInitComplete(srv gnet.Server) (action gnet.Action) {
	log.Printf("HTTP server is listening on %s (multi-cores: %t, loops: %d)\n",
		srv.Addr.String(), srv.Multicore, srv.NumEventLoop)
	return
}

// 获取frame的内容
func (hs *httpServer) React(frame []byte, c gnet.Conn) (out []byte, action gnet.Action) {
	if c.Context() != nil {
		// bad thing happened
		out = errMsgBytes
		action = gnet.Close
		return
	}
	// handle the request
	out = frame
	return // 返回 out
}

func main() {
	var port int
	var multicore bool

	// Example command: go run http.go --port 8080 --multicore=true   multicore:多核
	flag.IntVar(&port, "port", 8080, "server port")
	flag.BoolVar(&multicore, "multicore", true, "multicore")
	flag.Parse()

	res = "Hello World!\r\n"

	http := new(httpServer)
	hc := new(httpCodec)

	//gnet.Serve(http, fmt.Sprintf("tcp://:%d/api/v1", port), gnet.WithMulticore(multicore), gnet.WithCodec(hc))
	// Start serving!
	log.Fatal(gnet.Serve(http, fmt.Sprintf("tcp://:%d", port), gnet.WithMulticore(multicore), gnet.WithCodec(hc)))
}

// curl  127.0.0.1:8080 -I
// 可以看到以下定义的信息

// appendHandle handles the incoming request and appends the response to
// the provided bytes, which is then returned to the caller.
//appendHandle处理传入的请求并将响应附加到所提供的字节，然后返回给调用方。
func appendHandle(b []byte, res string) []byte {
	return appendResp(b, "200 OK zhaoyi ok", "", res)
}

// appendResp will append a valid http response to the provide bytes.
// The status param should be the code plus text such as "200 OK".
// The head parameter should be a series of lines ending with "\r\n" or empty.
//appendResp将向提供的字节追加有效的http响应。
//状态参数应为代码加上文本，如“200 OK”。
//head参数应该是以“\r\n”结尾或为空的一系列行。
func appendResp(b []byte, status, head, body string) []byte {
	b = append(b, "HTTP/1.1"...)
	b = append(b, ' ')
	b = append(b, status...)
	b = append(b, '\r', '\n')
	b = append(b, "Server: zhaoyiTest\r\n"...)
	b = append(b, "Date: "...)
	b = time.Now().AppendFormat(b, "Mon, 02 Jan 2006 15:04:05 GMT")
	b = append(b, '\r', '\n')
	if len(body) > 0 {
		b = append(b, "Content-Length: "...)
		b = strconv.AppendInt(b, int64(len(body)), 10)
		b = append(b, '\r', '\n')
	}
	b = append(b, head...)
	b = append(b, '\r', '\n')
	if len(body) > 0 {
		b = append(b, body...)
	}
	return b
}

// byte to string
func b2s(b []byte) string {
	return *(*string)(unsafe.Pointer(&b))
}

// parseReq is a very simple http request parser. This operation
// waits for the entire payload to be buffered before returning a
// valid request.
//parseReq是一个非常简单的http请求解析器。此操作在返回有效请求之前等待缓冲整个负载。
//确实用在pipeline
func parseReq(data []byte, req *request) (leftover []byte, err error) {
	sdata := b2s(data)
	var i, s int
	var head string
	var clen int
	var q = -1
	// method, path, proto line
	for ; i < len(sdata); i++ {
		if sdata[i] == ' ' {
			req.method = sdata[s:i]
			for i, s = i+1, i+1; i < len(sdata); i++ {
				if sdata[i] == '?' && q == -1 {
					q = i - s
				} else if sdata[i] == ' ' {
					if q != -1 {
						req.path = sdata[s:q]
						req.query = req.path[q+1 : i]
					} else {
						req.path = sdata[s:i]
					}
					for i, s = i+1, i+1; i < len(sdata); i++ {
						if sdata[i] == '\n' && sdata[i-1] == '\r' {
							req.proto = sdata[s:i]
							i, s = i+1, i+1
							break
						}
					}
					break
				}
			}
			break
		}
	}
	if req.proto == "" {
		return data, fmt.Errorf("malformed request")
	}
	head = sdata[:s]
	for ; i < len(sdata); i++ {
		if i > 1 && sdata[i] == '\n' && sdata[i-1] == '\r' {
			line := sdata[s : i-1]
			s = i + 1
			if line == "" {
				req.head = sdata[len(head)+2 : i+1]
				i++
				if clen > 0 {
					if len(sdata[i:]) < clen {
						break
					}
					req.body = sdata[i : i+clen]
					i += clen
				}
				return data[i:], nil
			}
			if strings.HasPrefix(line, "Content-Length:") {
				n, err := strconv.ParseInt(strings.TrimSpace(line[len("Content-Length:"):]), 10, 64)
				if err == nil {
					clen = int(n)
				}
			}
		}
	}
	// not enough data
	return data, nil
}
