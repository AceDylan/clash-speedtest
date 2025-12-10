package main

import (
	"context"
	"encoding/json"
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

// Result 测试结果结构
type Result struct {
	Name        string
	Status      string
	Region      string
	Info        string
	ExitCountry string
}

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
	results := make([]Result, 0)
	bar := progressbar.Default(int64(len(proxies)), "测试中...")

	for _, proxy := range proxies {
		// 创建 HTTP 客户端
		client := createClient(proxy, *timeout)

		// 测试 YouTube
		result := unlock.TestYouTube(client)

		// 获取出口国家 (使用更可靠的 API)
		exitCountry := getExitCountry(client)

		results = append(results, Result{
			Name:        proxy.Name(),
			Status:      result.Status,
			Region:      result.Region,
			Info:        result.Info,
			ExitCountry: exitCountry,
		})

		bar.Add(1)
	}

	// 输出结果
	fmt.Println("\n")
	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"节点名称", "YouTube 状态", "区域", "出口国家", "备注"})
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
		exitCountryStr := result.ExitCountry
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

		// 出口国家着色
		if exitCountryStr != "" && exitCountryStr != "N/A" {
			exitCountryStr = colorYellow + exitCountryStr + colorReset
		}

		table.Append([]string{
			result.Name,
			statusStr,
			regionStr,
			exitCountryStr,
			noteStr,
		})
	}

	table.Render()
	fmt.Printf("\n总计: %d 个节点, %d 个可解锁 YouTube (%.1f%%)\n",
		len(results), successCount, float64(successCount)/float64(len(results))*100)

	// 生成有问题的节点列表文件（失败或国家不匹配）
	outputFile := "youtube_cn.txt"
	absPath, _ := filepath.Abs(outputFile)

	// 统计有问题的节点数量
	problematicCount := 0
	for _, result := range results {
		// 国家不匹配的节点总是计入
		if isCountryMismatch(result.Name, result.ExitCountry) {
			problematicCount++
			continue
		}
		// 解锁失败的节点：只有当出口国家已知时才计入
		if result.Status != "Success" {
			if result.ExitCountry == "" || result.ExitCountry == "N/A" {
				continue // 解锁失败且出口国未知时跳过
			}
			problematicCount++
		}
	}

	if problematicCount > 0 {
		err := saveProblematicNodes(results, outputFile)
		if err != nil {
			fmt.Printf("\n保存问题节点列表出错: %v\n", err)
		} else {
			fmt.Printf("\n已将 %d 个问题节点（失败或国家不匹配）保存到: %s\n", problematicCount, absPath)
		}
	} else {
		// 删除旧的列表文件，避免过时数据
		if _, err := os.Stat(outputFile); err == nil {
			os.Remove(outputFile)
			fmt.Printf("\n所有节点均正常（解锁成功且国家匹配），已删除旧的问题列表: %s\n", absPath)
		} else {
			fmt.Println("\n所有节点均正常（解锁成功且国家匹配）")
		}
	}
}

// countryNameMap 国家/地区名称到代码的映射
var countryNameMap = map[string]string{
	"美国": "US", "美": "US", "US": "US",
	"香港": "HK", "港": "HK", "HK": "HK",
	"台湾": "TW", "台": "TW", "TW": "TW",
	"日本": "JP", "日": "JP", "JP": "JP",
	"韩国": "KR", "韩": "KR", "KR": "KR",
	"新加坡": "SG", "狮城": "SG", "新": "SG", "SG": "SG",
	"英国": "GB", "英": "GB", "UK": "GB", "GB": "GB",
	"德国": "DE", "德": "DE", "DE": "DE",
	"法国": "FR", "法": "FR", "FR": "FR",
	"加拿大": "CA", "加": "CA", "CA": "CA",
	"澳大利亚": "AU", "澳": "AU", "AU": "AU",
	"俄罗斯": "RU", "俄": "RU", "RU": "RU",
	"印度": "IN", "印": "IN", "IN": "IN",
	"巴西": "BR", "BR": "BR",
	"阿根廷": "AR", "AR": "AR",
	"土耳其": "TR", "TR": "TR",
	"荷兰": "NL", "NL": "NL",
	"意大利": "IT", "IT": "IT",
	"西班牙": "ES", "ES": "ES",
	"瑞士": "CH", "CH": "CH",
	"瑞典": "SE", "SE": "SE",
	"波兰": "PL", "PL": "PL",
	"马来西亚": "MY", "马": "MY", "MY": "MY",
	"泰国": "TH", "泰": "TH", "TH": "TH",
	"越南": "VN", "越": "VN", "VN": "VN",
	"菲律宾": "PH", "菲": "PH", "PH": "PH",
	"印尼": "ID", "印度尼西亚": "ID", "ID": "ID",
	"阿联酋": "AE", "迪拜": "AE", "AE": "AE",
	"南非": "ZA", "ZA": "ZA",
}

// getExpectedCountryFromName 从节点名称中提取预期的国家代码
func getExpectedCountryFromName(name string) string {
	// 只考虑 | 后面的部分，忽略前面的前缀（如 "ALPHA | 香港 01" -> "香港 01"）
	if idx := strings.Index(name, "|"); idx != -1 {
		name = name[idx+1:]
	}
	name = strings.TrimSpace(name)
	nameUpper := strings.ToUpper(name)

	// 将 map 的键按长度从长到短排序，避免短代码误匹配
	// 例如避免 "PH" 匹配到 "ALPHA" 中的 "PH"
	type keyValue struct {
		key  string
		code string
	}
	var sortedKeys []keyValue
	for key, code := range countryNameMap {
		sortedKeys = append(sortedKeys, keyValue{key, code})
	}

	// 按键长度从长到短排序
	for i := 0; i < len(sortedKeys); i++ {
		for j := i + 1; j < len(sortedKeys); j++ {
			if len(sortedKeys[i].key) < len(sortedKeys[j].key) {
				sortedKeys[i], sortedKeys[j] = sortedKeys[j], sortedKeys[i]
			}
		}
	}

	// 按长度顺序检查
	for _, kv := range sortedKeys {
		upperKey := strings.ToUpper(kv.key)
		if strings.Contains(nameUpper, upperKey) {
			return kv.code
		}
	}
	return ""
}

// isCountryMismatch 判断节点名称中的国家与出口国家是否不匹配
func isCountryMismatch(nodeName, exitCountry string) bool {
	if exitCountry == "" || exitCountry == "N/A" {
		return false // 无法获取出口国家时不认为是不匹配
	}

	expectedCountry := getExpectedCountryFromName(nodeName)
	if expectedCountry == "" {
		return false // 节点名称中没有明确的国家信息，不认为是不匹配
	}

	return expectedCountry != exitCountry
}

// saveProblematicNodes 保存有问题的节点列表到文件（失败或国家不匹配）
func saveProblematicNodes(results []Result, filename string) error {
	// 收集有问题的节点
	type problematicNode struct {
		result      Result
		countryCode string
	}

	var nodes []problematicNode
	for _, result := range results {
		// 保存国家不匹配的节点
		if isCountryMismatch(result.Name, result.ExitCountry) {
			countryCode := getExpectedCountryFromName(result.Name)
			if countryCode == "" {
				countryCode = "ZZ" // 未知国家放在最后
			}
			nodes = append(nodes, problematicNode{result, countryCode})
			continue
		}

		// 保存解锁失败但出口国已知的节点；出口国未知时跳过
		if result.Status != "Success" {
			if result.ExitCountry == "" || result.ExitCountry == "N/A" {
				continue // 解锁失败且出口国未知时跳过
			}
			countryCode := getExpectedCountryFromName(result.Name)
			if countryCode == "" {
				countryCode = "ZZ" // 未知国家放在最后
			}
			nodes = append(nodes, problematicNode{result, countryCode})
		}
	}

	if len(nodes) == 0 {
		return nil // 没有问题节点，不写入文件
	}

	// 按国家代码排序（冒泡排序）
	for i := 0; i < len(nodes); i++ {
		for j := i + 1; j < len(nodes); j++ {
			if nodes[i].countryCode > nodes[j].countryCode {
				nodes[i], nodes[j] = nodes[j], nodes[i]
			}
		}
	}

	// 写入文件
	var builder strings.Builder
	builder.WriteString("节点名称\tYouTube状态\t区域\t出口国家\n")

	for _, node := range nodes {
		result := node.result
		status := result.Status
		if status != "Success" {
			status = "Failed"
		}
		region := result.Region
		if region == "" {
			region = "N/A"
		}
		exitCountry := result.ExitCountry
		if exitCountry == "" {
			exitCountry = "N/A"
		}

		builder.WriteString(fmt.Sprintf("%s\t%s\t%s\t%s\n",
			result.Name, status, region, exitCountry))
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

// getExitCountry 获取代理的出口国家
func getExitCountry(client *http.Client) string {
	// 使用 ip-api.com (免费、可靠)
	req, err := http.NewRequest("GET", "http://ip-api.com/json/?fields=countryCode", nil)
	if err != nil {
		return "N/A"
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := client.Do(req)
	if err != nil {
		return "N/A"
	}
	defer resp.Body.Close()

	var result struct {
		CountryCode string `json:"countryCode"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "N/A"
	}

	if result.CountryCode != "" {
		return result.CountryCode
	}
	return "N/A"
}
