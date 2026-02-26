package main

import (
	"embed"
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
//
//go:embed frontend/*
var frontendFS embed.FS

// main 完成运行目录初始化、HTTP 服务启动和浏览器拉起。
func main() {
	// 确定数据目录：exe 目录下有 covers/ 就用 exe 目录，否则回退 cwd（兼容 go run）。
	baseDir := resolveBaseDir()

	// 如果 baseDir 下有 frontend/index.html，直接从磁盘读取，方便实时修改前端。
	frontend, devMode, err := loadFrontendFS(baseDir)
	if err != nil {
		log.Fatalf("加载前端文件失败: %v", err)
	}

	h, coverCount, err := server.NewHandler(baseDir, frontend)
	if err != nil {
		log.Fatalf("初始化服务器失败: %v", err)
	}

	url := fmt.Sprintf("http://localhost:%d", port)
	modeLabel := "Release (embedded)"
	if devMode {
		modeLabel = "Development (disk)"
	}
	printStartupBanner(modeLabel, url, coverCount)

	// 浏览器打开是辅助行为，不阻塞服务启动。
	go openBrowser(url)

	if err := http.ListenAndServe(fmt.Sprintf(":%d", port), h); err != nil {
		log.Fatalf("服务器启动失败: %v", err)
	}
}

// resolveBaseDir 确定数据根目录：exe 目录下有 covers/ 就用 exe 目录，否则回退 cwd（兼容 go run）。
func resolveBaseDir() string {
	cwd, err := os.Getwd()
	if err != nil {
		log.Fatalf("获取当前工作目录失败: %v", err)
	}

	execPath, err := os.Executable()
	if err != nil {
		log.Printf("获取可执行文件路径失败，使用当前目录: %v", err)
		return cwd
	}

	execDir := filepath.Dir(execPath)
	if info, statErr := os.Stat(filepath.Join(execDir, "covers")); statErr == nil && info.IsDir() {
		return execDir
	}

	return cwd
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

// loadFrontendFS 自动检测磁盘上的 frontend/ 目录，有则从磁盘读取（方便开发），否则用 embed。
func loadFrontendFS(baseDir string) (fs.FS, bool, error) {
	frontendDir := filepath.Join(baseDir, "frontend")
	diskFS := os.DirFS(frontendDir)
	if _, err := fs.Stat(diskFS, "index.html"); err == nil {
		return diskFS, true, nil
	}

	embeddedFS, err := fs.Sub(frontendFS, "frontend")
	if err != nil {
		return nil, false, err
	}
	return embeddedFS, false, nil
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
