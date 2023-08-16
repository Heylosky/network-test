package traceroute

import (
	"flag"
	"fmt"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
	"golang.org/x/sys/unix"
	"net/netip"
	"time"
)

// TraceResult 结果记录
type TraceResult struct {
	TTL         int
	NextHot     string
	ElapsedTime time.Duration
	Replied     bool
}

// TraceConfig 命令初始化
type TraceConfig struct {
	FirstTTL int
	Retry    int
	MaxTTL   int
	Debug    bool
	WaitSec  int64
}

// Traceroute有三种实现：UDP(Cisco 和 Linux 默认) ICMP(MS Windows) TCP(traceTcp)
// UDP实现：向外发送的是一个 UDP 数据包，final reply 是 ICMP Destination Unreachable
// ICMP实现：向外发送的是一个 ICMP Echo Request，final reply 是 ICMP Echo Reply
// TCP实现：向外发送的是 TCP SYN 数据包，这样做最大的好处就是穿透防火墙的几率更大因为 TCP SYN 看起来是试图建立一个正常的 TCP 连接
func main() {
	conf := &TraceConfig{
		Debug: true,
	}

	var destAddr string
	flag.IntVar(&conf.FirstTTL, "f", 1, "first ttl")
	flag.IntVar(&conf.MaxTTL, "m", 30, "max ttl")
	flag.IntVar(&conf.Retry, "r", 0, "retry time")
	flag.Int64Var(&conf.WaitSec, "w", 1, "wait seconds")

	flag.Parse()
	destAddr = flag.Arg(0)
	if destAddr == "" {
		usage()
		return
	}

	fmt.Printf("traceroute to %s %d hots max\n", destAddr, conf.MaxTTL)
	results, err := Traceroute(destAddr, conf)
	if err != nil {
		fmt.Println(err.Error())
		return
	}

	fmt.Printf("results:%+v\n", results)
}

func usage() {
	fmt.Println("usage: traceroute host(dest address ipv4 or ipv6)")
	fmt.Println("with config:traceroute [-f firstTTL] [-m maxTTL] [-r retryTimes] [-w wait seconds] host(ipv4 or ipv6)]")
}

func (conf *TraceConfig) check() error {
	if conf.MaxTTL <= 0 {
		return fmt.Errorf("invalid max ttl: %d", conf.MaxTTL)
	}

	if conf.FirstTTL <= 0 {
		conf.FirstTTL = DefaultFirstTTL
	}

	if conf.MaxTTL > DefaultMaxTTL {
		conf.MaxTTL = DefaultMaxTTL
	}

	if conf.WaitSec <= 0 {
		conf.WaitSec = DefaultMinWaitSec
	} else if conf.WaitSec >= DefaultMaxWaitSec {
		conf.WaitSec = DefaultMaxWaitSec
	}

	return nil
}

const (
	DesMinPort        = 33434
	DesMaxPort        = 33534
	DefaultFirstTTL   = 1
	DefaultMaxTTL     = 64
	DefaultMinWaitSec = 1
	DefaultMaxWaitSec = 10
)

func Traceroute(destIP string, conf *TraceConfig) ([]TraceResult, error) {
	addr, err := netip.ParseAddr(destIP)
	if err != nil {
		return nil, err
	}

	//若未输入conf信息，则初始化默认设置
	if conf == nil {
		conf = &TraceConfig{
			FirstTTL: 1,
			Retry:    0,
			MaxTTL:   30,
		}
	}
	if addr.Is4() {
		fmt.Printf("对目标路径进行跟踪:\n")
		return trace4(conf, addr)
	} else {
		return trace6(conf, addr)
	}
}

// trace4 对ipv4类地址进行路由跟踪
func trace4(conf *TraceConfig, addr netip.Addr) ([]TraceResult, error) {
	if !addr.Is4() {
		return nil, fmt.Errorf("invalid addr:%s", addr.String())
	}
	if err := conf.check(); err != nil {
		return nil, err
	}

	// 创建一个socket的文件描述，用于发送udp消息，func Socket(domain, typ, proto int) (fd int, err error)
	// unix.AF_INET 表示使用ipv4地址
	// unix.SOCK_DGRAM 数据报套接字类型，Datagram Socket，使用 UDP（User Datagram Protocol）协议进行通信
	// 流套接字（Stream Socket）：使用TCP协议提供可靠的、面向连接的数据流传输。 数据报套接字（Datagram Socket）：使用UDP协议提供无连接、不可靠的数据报传输。 原始套接字（Raw Socket）：允许开发人员直接访问底层网络协议，进行更底层的网络编程。
	// unix.IPPROTO_UDP 表示使用UDP协议
	sendSocket, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM, unix.IPPROTO_UDP)
	if err != nil {
		return nil, err
	}
	defer unix.Close(sendSocket)

	// 用于接收icmp消息
	// 原始套接字，直接访问底层网络协议，应用程序可以自定义和操作网络层（如IP层）和传输层（如TCP和UDP层）的协议头部
	recvSocket, err := unix.Socket(unix.AF_INET, unix.SOCK_RAW, unix.IPPROTO_ICMP)
	if err != nil {
		return nil, err
	}
	// 设置套接字的时间选项值，使用通用套接字选项层级SOL_SOCKET
	// &unix.Timeval设置了秒sec和微秒usec做为超时时间
	if err := unix.SetsockoptTimeval(recvSocket, unix.SOL_SOCKET, unix.SO_RCVTIMEO,
		&unix.Timeval{Sec: conf.WaitSec, Usec: 0}); err != nil {
		return nil, err
	}
	defer unix.Close(recvSocket)

	ttl := conf.FirstTTL
	try := conf.Retry
	destPort := DesMinPort
	destAddr := addr.As4()

	var results []TraceResult
	begin := time.Now()
	for {
		begin = time.Now()

		// 循环发送udp消息
		// func SetsockoptInt(fd, level, opt int, value int) (err error)
		// 该函数可以用于设定套接字选项的整数值，我们用来设定IP的头信息中的TTL；同时该函数可以设定socket的请求或者接收消息的超时时间
		// TTL 是 IP 协议头部的字段，而不是 TCP 报文头部的字段，因此套接字层级选择IPPROTO_IP层级，以控制IP头中的TTL。
		// 初始化ttl等于1
		if err := unix.SetsockoptInt(sendSocket, unix.IPPROTO_IP, unix.IP_TTL, ttl); err != nil {
			return nil, err
		}
		// func Sendto(fd int, p []byte, flags int, to Sockaddr) (err error)
		// 用于将数据通过套接字发送到目标地址，可用于发送数据报或数据流。它是面向底层的套接字 API，提供了对发送数据的细粒度控制
		// p 要发送的数据; flags 发送操作的标志，可以是可选的控制标志，例如 MSG_DONTWAIT（非阻塞发送）等; to 目标地址，是一个 unix.Sockaddr 类型的参数
		// 端口在33434到33534区间内循环
		if err := unix.Sendto(sendSocket, []byte{0}, 0, &unix.SockaddrInet4{Port: destPort, Addr: destAddr}); err != nil {
			return nil, err
		}

		var p = make([]byte, 4096)
		result := TraceResult{TTL: ttl, ElapsedTime: time.Since(begin), Replied: false}
		// 通过Recvfrom接收消息；判断接收消息是否报错，如果报错则直接退出循环并结束traceroute操作
		// n 接收到的数据的字节数
		// from 发送方的地址信息，是一个 unix.Sockaddr 类型的值，可以通过类型断言转换为具体的地址类型。
		n, from, err := unix.Recvfrom(recvSocket, p, 0)
		// 解析返回的ICMP消息
		// 由于ipv4的Header包头长度最小是20字节，最大是60字节，会出现浮动，因此需要拿到实际的ipv4头长度
		if err == nil {
			try = 0
			// 获取发送放ipv4地址
			fromAddr := from.(*unix.SockaddrInet4).Addr
			// 这里使用ipv4库的ParseHeader函数解析ipv4的包头结构，从而确定ip报文头部的长度
			// n为报文总长度
			ipHeader, err := ipv4.ParseHeader(p[:n])
			if err != nil {
				return nil, err
			}
			if ipHeader.Len > n {
				continue
			}
			// 然后截取ipHeader.Len长度到总长度n的中间部分，就得到完整的ICMP消息报文
			icmpReply, err := icmp.ParseMessage(1, p[ipHeader.Len:n])
			if err != nil {
				return nil, err
			}
			// 如果收到的是ICMPTypeTimeExceeded，则需要将发送方的地址（路由地址）存下来，并且将ttl+1，然后再次循环发送udp消息到目的地。
			// 如果收到的ICMP消息类型是ICMPTypeDestinationUnreachable或者ttl超过了最大的ttl设定或者接受的的ICMP消息来自于目的地址，则结束发包，并输出结果。
			if icmpReply.Type == ipv4.ICMPTypeTimeExceeded || icmpReply.Type == ipv4.ICMPTypeDestinationUnreachable {
				result.Replied = true
				result.NextHot = netip.AddrFrom4(fromAddr).String()
				results = append(results, result)
				if conf.Debug {
					fmt.Printf("ttl %d addr:%v time:%v \n", ttl, result.NextHot, time.Since(begin))
				}
				if icmpReply.Type == ipv4.ICMPTypeTimeExceeded {
					ttl++
				}
				if icmpReply.Type == ipv4.ICMPTypeDestinationUnreachable || ttl > conf.MaxTTL || fromAddr == destAddr {
					return results, nil
				}
			} else {
				fmt.Printf("%d unknown:%+v from:%+v\n", ttl, icmpReply, fromAddr)
			}
		} else {
			if conf.Debug {
				fmt.Printf("ttl %d * err:%s time:%v\n", ttl, err.Error(), time.Since(begin))
			}
			result.NextHot = "*"
			results = append(results, result)
			try++
			if try > conf.Retry {
				try = 0
				ttl++
			}
			if ttl > conf.MaxTTL {
				return results, nil
			}
		}

		// 端口在33434到33534的区间内循环
		destPort++
		if destPort > DesMaxPort {
			destPort = DesMinPort
		}
	}
}

func trace6(conf *TraceConfig, addr netip.Addr) ([]TraceResult, error) {
	if !addr.Is6() {
		return nil, fmt.Errorf("invalid addr:%s", addr.String())
	}
	if err := conf.check(); err != nil {
		return nil, err
	}
	ttl := conf.FirstTTL
	try := conf.Retry
	destPort := DesMinPort
	destAddr := addr.As16()

	recvSocket, err := unix.Socket(unix.AF_INET6, unix.SOCK_RAW, unix.IPPROTO_ICMPV6)
	if err != nil {
		return nil, fmt.Errorf("socket recv int failed:%s", err.Error())
	}
	sendSocket, err := unix.Socket(unix.AF_INET6, unix.SOCK_DGRAM, unix.IPPROTO_UDP)
	if err != nil {
		return nil, fmt.Errorf("socket send int failed:%s", err.Error())
	}
	defer unix.Close(recvSocket)
	defer unix.Close(sendSocket)

	if err := unix.SetsockoptTimeval(recvSocket, unix.SOL_SOCKET, unix.SO_RCVTIMEO,
		&unix.Timeval{Sec: conf.WaitSec, Usec: 0}); err != nil {
		return nil, fmt.Errorf("socket opt recv int failed:%s", err.Error())
	}

	var results []TraceResult
	begin := time.Now()
	for {
		begin = time.Now()
		if err := unix.SetsockoptInt(sendSocket, unix.IPPROTO_IPV6, unix.IPV6_UNICAST_HOPS, ttl); err != nil {
			return nil, fmt.Errorf("socket opt ttl int failed:%s", err.Error())
		}
		if err := unix.Sendto(sendSocket, []byte("hello"), 0, &unix.SockaddrInet6{Port: destPort, Addr: destAddr}); err != nil {
			return nil, fmt.Errorf("sendto failed:%s", err.Error())
		}

		var p = make([]byte, 4096)
		result := TraceResult{TTL: ttl, ElapsedTime: time.Since(begin), Replied: false}
		n, from, err := unix.Recvfrom(recvSocket, p, 0)
		if err == nil {
			try = 0
			icmpReply, err := icmp.ParseMessage(58, p[:n])
			if err != nil {
				return nil, fmt.Errorf("parse message failed:%s", err.Error())
			}

			if icmpReply.Type == ipv6.ICMPTypeTimeExceeded || icmpReply.Type == ipv6.ICMPTypeDestinationUnreachable {
				fromAddr := from.(*unix.SockaddrInet6).Addr
				result.Replied = true
				result.NextHot = netip.AddrFrom16(fromAddr).String()
				results = append(results, result)
				if conf.Debug {
					fmt.Printf("ttl %d receive from:%v time:%v icmpReply:%+v\n", ttl, result.NextHot, time.Since(begin), icmpReply)
				}

				if icmpReply.Type == ipv6.ICMPTypeTimeExceeded {
					ttl++
				}
				if icmpReply.Type == ipv6.ICMPTypeDestinationUnreachable || ttl > conf.MaxTTL || fromAddr == destAddr {
					return results, nil
				}
			}
		} else {
			if conf.Debug {
				fmt.Printf("ttl %d * err: %s \n", ttl, err.Error())
			}
			result.NextHot = "*"
			results = append(results, result)
			try++
			if try > conf.Retry {
				try = 0
				ttl++
			}
		}

		destPort++
		if destPort > DesMaxPort {
			destPort = DesMinPort
		}
	}
}
