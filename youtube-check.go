package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/faceair/clash-speedtest/speedtester"
	"github.com/faceair/clash-speedtest/unlock"
	"github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/log"
	"github.com/olekukonko/tablewriter"
	"github.com/schollz/progressbar/v3"
)

var (
	configPath  = flag.String("c", "", "配置文件路径，支持 http(s) 链接")
	filterRegex = flag.String("f", ".+", "使用正则表达式过滤节点名称")
	timeout     = flag.Duration("timeout", time.Second*10, "测试超时时间")
)

const (
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorReset  = "\033[0m"
)

func main() {
	flag.Parse()
	log.SetLevel(log.SILENT)

	fmt.Println("YouTube 解锁快速检测工具\n")

	if *configPath == "" {
		fmt.Println("请使用 -c 参数指定配置文件路径")
		fmt.Println("用法: go run youtube-check.go -c config.yaml")
		fmt.Println("     go run youtube-check.go -c 订阅链接")
		fmt.Println("     go run youtube-check.go -c config.yaml -f 'HK|港'  # 只测试香港节点")
		os.Exit(1)
	}

	// 创建测速器来加载代理
	tester := speedtester.New(&speedtester.Config{
		ConfigPaths: *configPath,
		FilterRegex: *filterRegex,
		Timeout:     *timeout,
	}, false)

	// 加载代理节点
	proxies, err := tester.LoadProxies()
	if err != nil {
		fmt.Printf("加载配置失败: %v\n", err)
		os.Exit(1)
	}

	if len(proxies) == 0 {
		fmt.Println("没有找到符合条件的节点")
		os.Exit(1)
	}

	fmt.Printf("找到 %d 个节点，开始测试...\n\n", len(proxies))

	// 测试结果
	type Result struct {
		Name   string
		Status string
		Region string
		Info   string
	}

	results := make([]Result, 0)
	bar := progressbar.Default(int64(len(proxies)), "测试中...")

	for _, proxy := range proxies {
		// 创建 HTTP 客户端
		client := createClient(proxy, *timeout)

		// 测试 YouTube
		result := unlock.TestYouTube(client)
		results = append(results, Result{
			Name:   proxy.Name(),
			Status: result.Status,
			Region: result.Region,
			Info:   result.Info,
		})

		bar.Add(1)
	}

	// 输出结果
	fmt.Println("\n")
	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"节点名称", "YouTube 状态", "区域", "备注"})
	table.SetAutoWrapText(false)
	table.SetAutoFormatHeaders(true)
	table.SetHeaderAlignment(tablewriter.ALIGN_LEFT)
	table.SetAlignment(tablewriter.ALIGN_LEFT)
	table.SetCenterSeparator("")
	table.SetColumnSeparator("")
	table.SetRowSeparator("")
	table.SetHeaderLine(false)
	table.SetBorder(false)
	table.SetTablePadding("\t")
	table.SetNoWhiteSpace(true)

	successCount := 0
	for _, result := range results {
		statusStr := result.Status
		regionStr := result.Region
		noteStr := ""

		if result.Status == "Success" {
			statusStr = colorGreen + "✓ 解锁" + colorReset
			if regionStr != "" && regionStr != "Available" {
				regionStr = colorGreen + regionStr + colorReset
			}
			successCount++
		} else {
			statusStr = colorRed + "✗ 失败" + colorReset
			regionStr = colorRed + "N/A" + colorReset
			if result.Info != "" {
				noteStr = colorRed + result.Info + colorReset
			}
		}

		table.Append([]string{
			result.Name,
			statusStr,
			regionStr,
			noteStr,
		})
	}

	table.Render()
	fmt.Printf("\n总计: %d 个节点, %d 个可解锁 YouTube (%.1f%%)\n",
		len(results), successCount, float64(successCount)/float64(len(results))*100)

	// 生成失败节点列表文件
	failedNodes := make([]string, 0)
	for _, result := range results {
		if result.Status != "Success" {
			failedNodes = append(failedNodes, result.Name)
		}
	}

	outputFile := "youtube_cn.txt"
	absPath, _ := filepath.Abs(outputFile)

	if len(failedNodes) > 0 {
		err := saveFailedNodes(failedNodes, outputFile)
		if err != nil {
			fmt.Printf("\n保存失败节点列表出错: %v\n", err)
		} else {
			fmt.Printf("\n已将 %d 个失败节点保存到: %s\n", len(failedNodes), absPath)
		}
	} else {
		// 删除旧的失败列表文件，避免过时数据
		if _, err := os.Stat(outputFile); err == nil {
			os.Remove(outputFile)
			fmt.Printf("\n所有节点均可解锁 YouTube，已删除旧的失败列表: %s\n", absPath)
		} else {
			fmt.Println("\n所有节点均可解锁 YouTube")
		}
	}
}

// saveFailedNodes 保存失败节点列表到文件
func saveFailedNodes(nodes []string, filename string) error {
	var builder strings.Builder
	for _, node := range nodes {
		builder.WriteString(node)
		builder.WriteString("\n")
	}
	return os.WriteFile(filename, []byte(builder.String()), 0644)
}

// createClient 创建一个通过代理的 HTTP 客户端
func createClient(proxy constant.Proxy, timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				host, port, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, err
				}
				var u16Port uint16
				if port, err := strconv.ParseUint(port, 10, 16); err == nil {
					u16Port = uint16(port)
				}
				return proxy.DialContext(ctx, &constant.Metadata{
					Host:    host,
					DstPort: u16Port,
				})
			},
		},
	}
}
