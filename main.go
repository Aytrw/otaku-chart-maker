package main

import (
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/Aytrw/otaku-chart-maker/internal/server"
)

// port 是本地 HTTP 服务监听端口。
const port = 8000

// frontendFS 在发布模式下提供嵌入的前端文件。
//go:embed frontend/*
var frontendFS embed.FS

// main 完成运行目录初始化、HTTP 服务启动和浏览器拉起。
func main() {
	// -dev 模式直接读取磁盘文件，便于前端热更新式调试。
	devMode := flag.Bool("dev", false, "开发模式：从 frontend/ 目录实时读取前端文件")
	flag.Parse()

	// 发布模式使用可执行文件目录，开发模式使用当前工作目录。
	execPath, err := os.Executable()
	if err != nil {
		log.Fatalf("获取可执行文件路径失败: %v", err)
	}

	baseDir := filepath.Dir(execPath)
	if *devMode {
		cwd, cwdErr := os.Getwd()
		if cwdErr != nil {
			log.Fatalf("获取当前工作目录失败: %v", cwdErr)
		}
		baseDir = cwd
	}

	frontend, err := loadFrontendFS(*devMode, baseDir)
	if err != nil {
		log.Fatalf("加载前端文件失败: %v", err)
	}

	h, coverCount, err := server.NewHandler(baseDir, frontend)
	if err != nil {
		log.Fatalf("初始化服务器失败: %v", err)
	}

	url := fmt.Sprintf("http://localhost:%d", port)
	modeLabel := "Release"
	if *devMode {
		modeLabel = "Development"
	}
	printStartupBanner(modeLabel, url, coverCount)

	// 浏览器打开是辅助行为，不阻塞服务启动。
	go openBrowser(url)

	if err := http.ListenAndServe(fmt.Sprintf(":%d", port), h); err != nil {
		log.Fatalf("服务器启动失败: %v", err)
	}
}

// openBrowser 按当前操作系统选择默认打开 URL 的命令。
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}

	_ = cmd.Start()
}

// loadFrontendFS 按运行模式返回前端文件系统。
func loadFrontendFS(devMode bool, baseDir string) (fs.FS, error) {
	if devMode {
		frontendDir := filepath.Join(baseDir, "frontend")
		diskFS := os.DirFS(frontendDir)
		if _, err := fs.Stat(diskFS, "index.html"); err != nil {
			return nil, fmt.Errorf("开发模式找不到 frontend/index.html: %w", err)
		}
		return diskFS, nil
	}

	embeddedFS, err := fs.Sub(frontendFS, "frontend")
	if err != nil {
		return nil, err
	}
	return embeddedFS, nil
}

// printStartupBanner 输出统一启动信息。
func printStartupBanner(modeLabel, url string, coverCount int) {
	fmt.Println("╔══════════════════════════════════════════╗")
	fmt.Println("║  Otaku Chart Maker - Local Server        ║")
	fmt.Println("╠══════════════════════════════════════════╣")
	fmt.Printf("║  %-40s║\n", "Mode: "+modeLabel)
	fmt.Printf("║  %-40s║\n", "URL:  "+url)
	fmt.Printf("║  %-40s║\n", fmt.Sprintf("Covers: covers/ (%d images)", coverCount))
	fmt.Println("║  Press Ctrl+C to stop                    ║")
	fmt.Println("╚══════════════════════════════════════════╝")
}
