<p align="center">
  <img src="https://capsule-render.vercel.app/api?type=waving&color=gradient&customColorList=6,11,20&height=180&section=header&text=Otaku%20Chart%20Maker&fontSize=42&fontColor=fff&animation=twinkling&fontAlignY=32&desc=✨%20あなたの推しを、一枚の表に。&descSize=18&descAlignY=52" width="100%" />
</p>

<p align="center">
  <a href="https://github.com/Aytrw/otaku-chart-maker/releases"><img src="https://img.shields.io/github/v/release/Aytrw/otaku-chart-maker?style=flat-square&label=Release&color=e4558b" alt="Release"></a>
  <img src="https://img.shields.io/badge/Go-1.26+-00ADD8?style=flat-square&logo=go&logoColor=white" alt="Go">
  <img src="https://img.shields.io/badge/License-MIT-22c55e?style=flat-square" alt="License">
  <a href="https://github.com/Aytrw/otaku-chart-maker/releases"><img src="https://img.shields.io/github/downloads/Aytrw/otaku-chart-maker/total?style=flat-square&label=Downloads&color=8b5cf6" alt="Downloads"></a>
  <img src="https://img.shields.io/badge/Zero_Dependencies-Single_Binary-ff6b6b?style=flat-square" alt="Single Binary">
</p>

<p align="center">
  <b>📊 生成你的专属 ACGN 生涯喜好表 —— 零依赖、单文件、下载即用</b>
</p>

<br>

## ✦ 这是什么？

一个本地运行的 **ACGN 个人喜好表生成器**。

从 [Bangumi](https://bgm.tv) 搜索你喜欢的动画、漫画、小说、Galgame，挑选封面填入 6×10 的分类网格，一键导出为精美的 PNG 图片 —— 在社交媒体展示你的二次元品味。

> 所有数据 **100% 存储在本地**，不联网上传，不追踪，不注册。

<br>

## ✦ 功能一览

| | 功能 | 描述 |
|:---:|---|---|
| 🔍 | **Bangumi 搜索** | 关键词搜索 + 50+ 题材标签浏览，支持多标签组合筛选 |
| 🏷️ | **智能分类** | 动画 / 漫画 / 小说 / Galgame 四大类型切换 |
| ⚡ | **极速翻页** | 前端分页缓存 + 预加载 + 请求去重，翻页零等待 |
| ✂️ | **封面编辑器** | 内置裁剪、旋转、翻转，全分辨率导出 |
| 🖼️ | **PNG 导出** | Canvas 绘制，像素级还原网格布局，一键保存 |
| 💾 | **自动保存** | 每次操作实时持久化，关闭重开不丢失 |
| 📁 | **本地封面库** | 支持拖入自有图片，与搜索下载的封面统一管理 |
| 🚀 | **开箱即用** | 单个二进制文件，无需安装运行时，双击即启 |

<br>

## ✦ 快速开始

### 方式一：下载可执行文件（推荐）

前往 [**Releases**](https://github.com/Aytrw/otaku-chart-maker/releases) 下载对应平台的二进制文件：

| 平台 | 文件 |
|:---|:---|
| Windows | `otaku-chart-maker-windows-amd64.exe` |
| macOS (Apple Silicon) | `otaku-chart-maker-darwin-arm64` |
| Linux | `otaku-chart-maker-linux-amd64` |

```
双击运行 → 浏览器自动打开 http://localhost:8000 → 开始创作 🎨
```

### 方式二：从源码构建

```bash
git clone https://github.com/Aytrw/otaku-chart-maker.git
cd otaku-chart-maker
go build -o otaku-chart-maker .
./otaku-chart-maker
```

<br>

## ✦ 使用指南

```
1. 点击网格中的任意格子，打开封面选择弹窗
2. 在「🔍 搜索」标签页中搜索作品（支持中/日/英名称）
3. 也可以通过「🏷️ 标签浏览」按题材标签筛选
4. 点击搜索结果下载封面、自动应用到格子
5. 可使用裁剪编辑器微调封面构图
6. 全部填好后，点击「💾 保存为图片」导出 PNG
```

<br>

## ✦ 项目结构

```
otaku-chart-maker/
├── main.go                # 入口：embed 前端、启动服务、打开浏览器
├── go.mod
├── frontend/
│   └── index.html         # 前端单文件（HTML + CSS + JS）
├── internal/
│   ├── server/server.go   # HTTP 路由、状态读写、封面管理
│   └── api/bangumi.go     # Bangumi API 客户端、内存缓存
├── covers/                # 封面图片存储（运行时生成）
└── state.json             # 网格状态持久化（运行时生成）
```

<br>

## ✦ 开发

```bash
# 开发模式：前端文件从磁盘实时读取，修改后刷新即可生效
go run . -dev

# 构建
go build -ldflags="-s -w" -trimpath -o otaku-chart-maker .

# 代码检查
go vet ./...
```

<br>

## ✦ 技术栈

| 层 | 技术 |
|:---|:---|
| 后端 | Go `net/http` · `embed` · 零第三方依赖 |
| 前端 | 原生 HTML / CSS / JS · Canvas API |
| 数据源 | [Bangumi API](https://bangumi.github.io/api/) |
| 分发 | 单二进制 · 跨平台（Windows / macOS / Linux） |

<br>

## ✦ Roadmap

- [ ] 多数据源聚合搜索（AniList · VNDB）
- [ ] SQLite 本地缓存，二次搜索秒出
- [ ] 自定义网格尺寸与分类标签
- [ ] 主题配色切换
- [ ] 拖拽排序格子
- [ ] 高清导出（2x / 3x）
- [ ] GitHub Actions 自动发布

<br>

## ✦ License

[MIT](LICENSE) © [Aytrw](https://github.com/Aytrw)

---

<p align="center">
  <sub>如果这个项目对你有帮助，请给一个 ⭐ Star —— 这是最好的鼓励！</sub>
</p>

<p align="center">
  <img src="https://capsule-render.vercel.app/api?type=waving&color=gradient&customColorList=6,11,20&height=100&section=footer" width="100%" />
</p>
