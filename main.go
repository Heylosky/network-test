package main

import (
	"fmt"
	"github.com/cheggaaa/pb/v3"
	"github.com/jedib0t/go-pretty/v6/table"
	"gitlab.com/curl/icmp"
	"gitlab.com/curl/logger"
	"gitlab.com/curl/traceroute"
	"go.uber.org/zap"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

var urlString, host, port string
var curlR, tcpR, icmpR bool

/*
main
（七层）通过net/http模拟curl请求，可模拟get和post
（四层）创建tcp连接请求，判断目标主机地址加端口是否畅通；
（三层）若不通则进行ICMP探测主机地址是否可达(ping)；再通过udp实现跟踪路由情况(traceRoute)；
*/
func main() {
	logger.InitLogger()
	/*// input example 'sudo go run oneTT.go -u https://www.baidu.com:7382'
	flag.StringVar(&urlString, "u", "", "The url needs to be test")
	flag.Parse()*/
	urlString = os.Getenv("URL_STRING")

	// 如果没有命令行参数
	if urlString == "" {
		fmt.Println("URL_STRING is empty!")
		zap.L().Fatal("URL_STRING is empty.")
	}

	// 从url分解出目的地址和端口号
	if urlString != "" {
		host, port = getHostFromURL(urlString)
		if port == "" {
			if strings.HasPrefix(urlString, "https://") {
				port = "443"
			} else if strings.HasPrefix(urlString, "http://") {
				port = "80"
			} else {
				log.Fatal("missing port in address, please use -p to specify")
			}
		}
	}

	zap.L().Info("目标: " + urlString)
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.AppendHeader(table.Row{"目标网络", "Address", "Host", "Port"})
	t.AppendRow([]interface{}{1, urlString, host, port})
	t.Render()

	// 模拟curl请求
	curlR = curl(urlString)
	tcpR = curlR
	icmpR = curlR

	// 模拟tcp到端口的请求
	if curlR == false {
		tcpR = telnet()
	}

	//TCP连接失败，进行ICMP探测
	if tcpR == false {
		fmt.Println("执行ICMP请求:")
		if icmpR = icmp.Icmp(host); icmpR == false {
			//通过traceroute跟踪路由情况
			traceRoute()
		}
	}

	t2 := table.NewWriter()
	t2.SetOutputMirror(os.Stdout)
	t2.AppendHeader(table.Row{"目标网络", "curl", "端口能通", "地址可达"})
	t2.AppendRow([]interface{}{urlString, curlR, tcpR, icmpR})
	t2.Render()
}

func getHostFromURL(address string) (host0, port0 string) {
	// 去除URL的前缀
	prefixes := []string{"http://", "https://"}
	for _, prefix := range prefixes {
		if strings.HasPrefix(address, prefix) {
			address = strings.TrimPrefix(address, prefix)
			break
		}
	}

	// 去除URL的路径和参数
	address = strings.Split(address, "/")[0]
	address = strings.Split(address, "?")[0]
	host0 = strings.Split(address, ":")[0]
	if len(strings.Split(address, ":")) > 1 {
		port0 = strings.Split(address, ":")[1]
	}
	return host0, port0
}

// traceRoute UDP trace
func traceRoute() {
	// dns解析域名
	ip := net.ParseIP(host).String()
	if ip != "<nil>" {
		fmt.Println("Host IP: ", ip)
	} else {
		ips, err := net.LookupIP(host)
		if err != nil {
			fmt.Println("DNS lookup failed:", err)
			return
		}
		ip = ips[0].String()
	}

	conf := &traceroute.TraceConfig{
		FirstTTL: 1,
		MaxTTL:   30,
		Retry:    0,
		WaitSec:  1,
		Debug:    true,
	}
	_, err := traceroute.Traceroute(ip, conf)
	if err != nil {
		fmt.Println(err.Error())
		return
	}
}

func curl(url string) (r bool) {
	count := 5000
	bar := pb.Full.Start(count)
	bar.SetWidth(100)
	bar.SetTemplateString(`{{ "执行curl请求:" }} {{ bar . "<" "=" (cycle . "↖" "↗" "↘" "↙" ) "." ">"}}`) // 自定义模板

	// 创建一个通道来在请求成功时发送信号
	successCh := make(chan bool)

	go func() {
		// 请求完成后关闭通道，无论成功与否
		defer close(successCh)

		// 创建一个HTTP客户端
		client := &http.Client{
			Timeout: 10 * time.Second,
		}

		// 发送get请求并获取响应
		resp, err := client.Get(url)
		if err != nil {
			zap.L().Error("curl请求失败", zap.Error(err))
			return
		}
		defer resp.Body.Close()

		// 读取响应内容
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			zap.L().Error("读取响应页面失败:", zap.Error(err))
		}
		// 输出响应内容
		fmt.Println(string(body))

		// 如果请求成功，发送成功信号到通道
		if resp.StatusCode == http.StatusOK {
			successCh <- true
		}
	}()

	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	timeoutCh := time.After(5 * time.Second)
	// 循环等待通道信号，或者超时
	for {
		select {
		case <-successCh:
			bar.Finish()
			fmt.Printf("%s\033[1;32;40m%s\033[0m\n", "curl请求:", "OK")
			zap.L().Info("curl请求成功")
			return true
		case <-ticker.C:
			bar.Increment()
		case <-timeoutCh:
			bar.Finish()
			fmt.Printf("%s\033[1;31;40m%s\033[0m\n", "curl请求:", "FAILED")
			return false
		}
	}
}

func telnet() (r bool) {
	count := 10000
	bar := pb.Full.Start(count)
	bar.SetWidth(100)
	bar.SetTemplateString(`{{ "执行TCP请求:" }} {{ bar . "<" "=" (cycle . "↖" "↗" "↘" "↙" ) "." ">"}}`)
	finishCh := make(chan bool)

	go func() {
		// 创建一个TCP连接，并设置连接超时时间为5秒
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), 8*time.Second)
		if err != nil {
			// 检查连接是否被拒绝，如果被拒绝那么说明网络畅通
			if opErr, ok := err.(*net.OpError); ok && opErr.Err.Error() == "connect: connection refused" {
				finishCh <- true
			} else {
				zap.L().Error("TCP连接测试:FAILED", zap.Error(err))
			}
			return
		}
		defer conn.Close()
	}()

	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	timeoutCh := time.After(10 * time.Second)
	for {
		select {
		case <-ticker.C:
			bar.Increment()
		case <-finishCh:
			bar.Finish()
			fmt.Printf("%s\033[1;32;40m%s\033[0m\n", "TCP连接测试:", "OK")
			zap.L().Info("TCP连接测试:OK")
			return true
		case <-timeoutCh:
			bar.Finish()
			fmt.Printf("%s\033[1;31;40m%s\033[0m\n", "TCP连接测试:", "FAILED")
			return false
		}
	}
}
