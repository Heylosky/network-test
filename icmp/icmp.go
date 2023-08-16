package icmp

import (
	"fmt"
	"go.uber.org/zap"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"log"
	"net"
	"os"
	"time"
)

const (
	icmpProtocolIPv4 = 1
	icmpProtocolIPv6 = 58
)

func Icmp(target string) (ok bool) {
	timeout := 5 * time.Second

	// 创建 ICMP 套接字
	conn, err := icmp.ListenPacket("ip4:icmp", "0.0.0.0")
	if err != nil {
		zap.L().Fatal("Error creating ICMP socket:", zap.Error(err))
	}
	defer conn.Close()

	// 解析目标主机的 IP 地址
	ipAddr, err := net.ResolveIPAddr("ip", target)
	if err != nil {
		zap.L().Error("Error resolving target IP address:", zap.Error(err))
		return false
	}

	// 构造 ICMP Echo 请求报文
	message := "Hello, Ping!"
	echo := icmp.Message{
		Type: ipv4.ICMPTypeEcho, // 或者 ipv6.ICMPTypeEchoRequest
		Code: 0,
		Body: &icmp.Echo{
			ID:   os.Getpid() & 0xffff,
			Seq:  1,
			Data: []byte(message),
		},
	}

	// 将 ICMP Echo 请求报文序列化为字节切片
	icmpBytes, err := echo.Marshal(nil)
	if err != nil {
		zap.L().Fatal("Error serializing ICMP message:", zap.Error(err))
		return false
	}

	// 发送 ICMP Echo 请求
	startTime := time.Now()
	if _, err := conn.WriteTo(icmpBytes, ipAddr); err != nil {
		zap.L().Error("Error sending ICMP message:", zap.Error(err))
		return false
	}

	// 设置读取超时时间
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		zap.L().Error("Error setting read deadline:", zap.Error(err))
		return false
	}

	// 接收 ICMP Echo 回复
	replyBytes := make([]byte, 1500)
	n, peer, err := conn.ReadFrom(replyBytes)
	if err != nil {
		fmt.Printf("%s\u001B[1;31;40m%s\u001B[0m\n", "ICMP到目标IP状态:", "FAILED")
		zap.L().Error("接收ICMP返回值失败", zap.Error(err))
		return false
	}
	duration := time.Since(startTime)

	// 解析 ICMP Echo 回复
	reply, err := icmp.ParseMessage(icmpProtocolIPv4, replyBytes[:n])
	if err != nil {
		log.Fatal("Error parsing ICMP reply:", err)
	}

	var meaning string
	// 检查回复类型和代码是否符合预期
	// ICMP消息体包括type，code，checksum，body
	// type 0 回显应答；3 目的不可达；5 重定向；8 请求回显
	// 如果不是0，则说明回显失败
	if reply.Type != ipv4.ICMPTypeEchoReply { // 或者 ipv6.ICMPTypeEchoReply
		log.Fatal("Unexpected ICMP reply type:", reply.Type)
	} else if reply.Type == ipv4.ICMPTypeDestinationUnreachable {
		switch reply.Code {
		case 0:
			meaning = "网络不可达"
		case 1:
			meaning = "主机不可达"
		case 2:
			meaning = "协议不可达"
		case 3:
			meaning = "端口不可达"
		}
	}

	// 如果目标主机禁icmp，那么不会回复icmp应答，因此也无法获得type3型回复
	// 输出结果
	fmt.Printf("Reply from %s (%s): bytes=%d time=%v\n", target, peer.String(), n, duration)
	if meaning != "" {
		fmt.Println(meaning)
	}
	fmt.Printf("%s\u001B[1;32;40m%s\u001B[0m\n", "ICMP到目标IP状态:", "OK")
	zap.L().Info("ICMP到目标ip可达")
	return true
}
