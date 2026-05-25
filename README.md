# L4D2Fix DXVK + L4N

用于《Left 4 Dead 2》的 Go + Win32 便携工具。程序会按补丁目录相对路径备份并覆盖游戏源文件，同时写入 Steam 启动参数，并提供一键清理还原。

## 功能

- 自动安装 `VC_redist.x86.exe` / `VC_redist.x64.exe`
- 从 Steam 库自动定位 L4D2，找不到时使用 Everything 辅助搜索
- 支持通用补丁 `L4N_dxvk2.7.1`
- 支持 AMD 专用补丁 `L4N_dxvk2.3.1_AMD`
- 覆盖前逐文件备份，记录到 exe 同级 `.l4n_auto_backup/`
- 一键清理可恢复游戏文件和 Steam 配置
- 可备份/恢复 `left4dead2/addons` 下的 MOD 文件
- 备份/恢复 MOD 时会同时处理显示设置 `left4dead2/cfg/video.txt`
- 自动写入 Steam AppID `550` 启动参数
- UI 内置中文使用说明、启动项和验证指令

## 使用

下载或准备便携目录后，双击运行：

```text
L4N_Go_Win32_Portable/L4N_Go_Win32.exe
```

按钮说明：

UI 上的六个按钮分为三列两行：

```text
通用处理    备份MOD    一键清理
AMD处理     恢复MOD    系统字体/游戏默认
```

- `通用处理`：使用 `resources/L4N_dxvk2.7.1`
- `AMD处理`：使用 `resources/L4N_dxvk2.3.1_AMD`
- `备份MOD`：复制游戏 `left4dead2/addons` 到 `resources/addons_backup`，并备份 `left4dead2/cfg/video.txt`
- `恢复MOD`：复制 `resources/addons_backup` 到游戏 `left4dead2/addons`，同名文件会覆盖，并恢复 `left4dead2/cfg/video.txt`
- `一键清理`：按 `.l4n_auto_backup/manifest.json` 还原游戏文件和 Steam 配置
- `系统字体/游戏默认`：通用处理后切换 `left4dead2/neko/config.vdf` 中的 `Tahoma` 字体替换行；未执行通用处理时会提示先进行通用处理

建议先关闭 Steam 和游戏，再执行处理或清理。

Steam 启动参数：

```text
-heapsize 2097152 -processheap -high -novid -nojoy -steam -lv -vulkan
```

游戏内验证：

```text
mat_info
ShaderAPI: shaderapivk

mem_dump
2,048.00MB
```

## 便携目录结构

发布版是非单文件便携版，除 exe 外的资源统一放入 `resources/`：

```text
L4N_Go_Win32_Portable/
  L4N_Go_Win32.exe
  resources/
    VC_redist.x86.exe
    VC_redist.x64.exe
    启动项指令【四】.txt
    验证指令【六】.txt
    tools/
      Everything/
    L4N_dxvk2.7.1/
    L4N_dxvk2.3.1_AMD/
    addons_backup/              # 运行“备份MOD”后生成
    display_settings_backup/    # 运行“备份MOD”后生成
```

运行后的备份和记录会写到：

```text
L4N_Go_Win32_Portable/.l4n_auto_backup/
```

不要改名或移动 `resources/`。

## 构建

生成图标：

```powershell
go run .\cmd\makeicon 001.jpg app.ico
rsrc -ico app.ico -manifest app.manifest -o rsrc.syso
```

构建 GUI exe：

```powershell
go test ./...
go build -ldflags="-H=windowsgui" -o L4N_Go_Win32.exe .
```

整理便携版时，将生成的 `L4N_Go_Win32.exe` 放在便携目录根部，将运行库、Everything、补丁目录和 txt 说明放入 `resources/`。

## Git 仓库内容

本仓库只保存源码、配置和小体积资源。大文件不进入 Git，避免触发 GitHub 文件体积限制。

已排除的主要内容：

- `VC_redist*.exe`
- `*.zip` / `*.7z`
- `*.exe` / `*.dll` / `*.vcs` / `*.syso`
- `tools/Everything/`
- `L4N_Go_Win32_Portable/`
- `L4N_单文件便携版/`
- 原始补丁目录和补丁压缩包
- Go 缓存、临时目录和 `.l4n_auto_backup/`

便携包和补丁资源建议通过 GitHub Release 附件分发。

## 来源与致谢

本项目中相当一部分资源整理方式和处理方法参考自以下 Bilibili 视频：

https://www.bilibili.com/video/BV1oHoMBbE7m/?spm_id_from=333.337.search-card.all.click&vd_source=7fbb056ce8209fd91e3f904175a597fa

感谢原视频作者对 L4D2、L4N 和 DXVK 配置流程的分享。本项目在此基础上整理为 Go + Win32 便携工具，并补充了自动定位、备份还原、Steam 启动项写入和便携目录管理等自动化流程。

字体切换功能参考自 Bilibili 视频《保姆级求生之路2利用L4N平台修改游戏字体以及进行武器检视》：

https://www.bilibili.com/video/BV1JpCjB9Ewp/?spm_id_from=333.337.search-card.all.click&vd_source=7fbb056ce8209fd91e3f904175a597fa

本工具将视频中的 L4N 字体替换思路整理为按钮操作：检测通用处理生成的 `left4dead2/neko/config.vdf`，并在系统字体与游戏默认字体之间切换。
