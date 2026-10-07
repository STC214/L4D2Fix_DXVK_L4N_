# L4D2Fix DXVK + L4N

用于《Left 4 Dead 2》的 Go + Win32 便携工具。主程序会按补丁目录的相对路径备份并覆盖游戏文件，写入 Steam 启动参数，并提供一键清理、MOD 备份恢复和字体切换功能。

> 建议使用前先关闭 Steam 和游戏。处理完成后再启动 Steam，可避免 Steam 退出时覆盖启动参数或配置文件。

## 目录

- [功能概览](#功能概览)
- [快速使用](#快速使用)
- [独立字体切换程序](#独立字体切换程序)
- [Steam 启动参数](#steam-启动参数)
- [游戏内验证](#游戏内验证)
- [便携目录结构](#便携目录结构)
- [构建](#构建)
- [Git 仓库内容](#git-仓库内容)
- [来源与致谢](#来源与致谢)

## 功能概览

界面顶部显示当前游戏的 DXVK、L4N 已安装版本（按实际 DLL SHA256 匹配安装记录或资源包；L4N 还读取已确认产品名称的 DLL 内嵌版本信息，不使用下拉框选择值）。没有相关文件显示“未使用”，已检测到但未匹配的文件显示“版本未知”，多个包使用相同 DLL 时列出候选。启动和每次操作后自动刷新，也可点击“刷新版本”。检测在后台进行，不加载游戏 DLL；这里只检查磁盘文件，不等同于验证游戏进程已启用补丁。

| 类别 | 能力 |
| --- | --- |
| 补丁处理 | 从 `dxvk/` 自动读取所有 DXVK 子目录，选择版本后一键安装 L4N 与对应 DXVK 文件。 |
| 自动定位 | 从 Steam 库自动定位 L4D2，找不到时使用 Everything 辅助搜索。 |
| 运行环境 | 自动安装 `VC_redist.x86.exe` / `VC_redist.x64.exe`。 |
| 安全回滚 | 覆盖前逐文件备份，记录到 exe 同级 `.l4n_auto_backup/`。 |
| 一键清理 | 可恢复游戏文件和 Steam 配置。 |
| MOD 管理 | 可备份/恢复 `left4dead2/addons`，并同步处理 `left4dead2/cfg/video.txt`。 |
| 启动项 | 从 L4N 资源中的启动指令 TXT 提取并写入 Steam AppID `550`，缺少此类文件时使用公共文件或默认参数。 |
| 字体工具 | 提供 L4N 字体切换和旧版 `Font_change` 字体切换工具。 |
| 使用说明 | UI 内置中文使用说明、启动项和验证指令。 |

## 快速使用

### 现有 L4N 配置留档

一键处理覆盖游戏文件前，会检查游戏中的 `left4dead2/neko/config.vdf`。有本工具安装记录时，与上次安装的配置 SHA256 比较；没有记录时，与所选资源包的配置比较，不一致就保守留档（这不一定代表用户修改，也可能是版本差异）。

留档位于 `resources/config_backups/<游戏路径标识>/<配置SHA256>/config.vdf`，同目录 `metadata.json` 记录来源、时间、哈希及比较基准。同一游戏的相同内容不会重复留档，不同内容分别保留。一键清理前也会保留安装后修改的配置。留档失败会停止覆盖或还原；留档独立于 `.l4n_auto_backup`，不会随一键清理删除，也不会自动覆盖回游戏。发布打包会排除个人留档。

安装与还原前检查备份清单及原文件备份；清单损坏、归属其他游戏目录、原备份缺失或哈希不一致时停止操作，避免覆盖原备份。旧版没有哈希的清单仍兼容，但只能验证备份是否可读。

下载或准备便携目录后，双击运行：

```text
L4N_Go_Win32_Portable/L4N_Go_Win32.exe
```

主程序左侧操作区带有线框分组，包含 DXVK 版本下拉菜单和常用操作按钮；右侧为随程序内置的使用说明：

```text
处理选项
  DXVK版本    一键处理
  备份MOD     恢复MOD    一键清理    系统字体/游戏默认
```

| 控件 | 作用 |
| --- | --- |
| `DXVK版本` | 自动列出 `dxvk/` 下所有可用子目录名，例如 `dxvk-2.3`、`dxvk-2.7.1`。 |
| `一键处理` | 使用所选 L4N 目录或压缩包作为基础补丁（默认保留原有 `resources/l4n/L4N_dxvk2.7.1`），并将所选 DXVK 版本映射为 `dxgi.dll`、`dxvk_d3d9.dll`、`bin/dxvk_d3d9.dll` 后覆盖到游戏目录。 |
| `备份MOD` | 复制游戏 `left4dead2/addons` 到 `resources/addons_backup`，并备份 `left4dead2/cfg/video.txt`。 |
| `恢复MOD` | 复制 `resources/addons_backup` 回游戏 `left4dead2/addons`，同名文件会覆盖，并恢复 `left4dead2/cfg/video.txt`。 |
| `一键清理` | 按 `.l4n_auto_backup/manifest.json` 还原游戏文件和 Steam 配置。 |
| `系统字体/游戏默认` | 一键处理后切换 `left4dead2/neko/config.vdf` 中的 `Tahoma` 字体替换行；未执行一键处理时会提示先进行一键处理。 |

右侧说明会提示 DXVK 版本来源和支持的目录结构：

```text
dxvk/dxvk-x.x/x32/
dxvk/dxvk-x.x/dxvk-x.x/x32/
```

## 独立字体切换程序

便携目录根部包含两个单独的字体工具。两个工具都带有字体下拉框、字体预览、浏览字体文件、更换字体和恢复默认字体按钮。

| 程序 | 适用场景 | 修改位置 |
| --- | --- | --- |
| `L4N_Font_Change.exe` | 游戏已经使用 L4N 平台。 | `left4dead2/neko/config.vdf` |
| `L4D2_Font_Change.exe` | 旧的 `Font_change` 文件复制方式。 | `Font_change/FontMod.yaml` 和游戏根目录同名文件 |

### L4N_Font_Change.exe

用于游戏已经使用 L4N 平台的情况。此工具会自动定位 L4D2 游戏目录，并检查：

```text
left4dead2/neko/config.vdf
```

| 操作 | 行为 |
| --- | --- |
| `更换字体` | 读取下拉框最终确认的字体名，只修改 `config.vdf` 中生效的 `"Tahoma" "字体名"` 替换行。 |
| `恢复默认字体` | 将整个 `font` 配置块用 `//` 注释掉，使游戏回到默认字体。 |
| `浏览字体文件` | 选择用户自备字体文件并自动安装到当前 Windows 用户字体目录。安装后仍需在下拉框中确认字体，再点击更换字体。 |

注意事项：

- 如果没有执行过一键处理，`config.vdf` 不存在，工具会提示先进行一键处理。
- L4N 字体切换只会启用或修改真正生效的 `"Tahoma" "字体名"` 替换行。
- 配置原文中已经注释的说明行、示例行和备用字体行会始终保持注释状态。
- 恢复默认字体后再次启用时，只移除工具给整个 `font` 配置块添加的最外层注释，不会误解除原文中已有的注释。

> L4N 字体工具不会把原文里的示例注释行改成生效配置。它只处理当前真正用于替换字体的 `Tahoma` 行。

### L4D2_Font_Change.exe

用于旧的 `Font_change` 文件复制方式。游戏使用 L4N 平台时此方式无效，L4N 平台请使用 `L4N_Font_Change.exe` 或主程序中的 `系统字体/游戏默认`。

| 操作 | 行为 |
| --- | --- |
| `更换字体` | 读取下拉框最终确认的字体名，写入 `Font_change/FontMod.yaml` 中 `fonts.Tahoma.name`，然后把 `Font_change` 目录内的文件按原相对路径复制到 L4D2 游戏根目录。 |
| `恢复默认字体` | 扫描 `Font_change` 目录内的文件列表，只删除游戏目录中与 `Font_change` 内相同相对路径的文件，不删除项目内的 `Font_change` 文件。 |
| `浏览字体文件` | 选择用户自备字体文件并自动安装。安装后仍需在下拉框中确认字体，再点击更换字体。 |

为避免字体授权和再分发问题，便携包不再内置字体文件。`Font_change/fonts/` 内的字体文件是可选资源，不再是便携包运行条件；默认配置使用系统常见字体 `Microsoft YaHei`。

字体选择规则：

- 下拉框会列出当前系统已安装字体。
- 支持手动输入字体名，并自动匹配已安装字体。
- 输入不存在的字体名时会阻止继续更换。

> 使用 L4N 平台时，不要用 `L4D2_Font_Change.exe` 作为字体方案；它的文件复制方式不会在 L4N 字体流程中生效。

## Steam 启动参数

主程序会写入 Steam AppID `550` 的启动参数：

```text
-heapsize 2097152 -processheap -high -novid -nojoy -steam -lv -vulkan
```

## 游戏内验证

进入游戏后打开控制台，输入：

```text
mat_info
```

期望看到：

```text
ShaderAPI: shaderapivk
```

再输入：

```text
mem_dump
```

期望看到：

```text
2,048.00MB
```

## 便携目录结构

发布版是非单文件便携版。exe 放在便携目录根部，运行资源统一放入 `resources/`：

```text
L4N_Go_Win32_Portable/
  L4N_Go_Win32.exe
  L4N_Font_Change.exe
  L4D2_Font_Change.exe
  resources/
    VC_redist.x86.exe
    VC_redist.x64.exe
    启动项指令【四】.txt
    验证指令【六】.txt
    tools/
      Everything/
    l4n/
      L4N_dxvk2.7.1/          # L4N 基础补丁或新版本压缩包
    dxvk/
      dxvk-2.3/
      dxvk-2.7.1/
    addons_backup/              # 运行“备份MOD”后生成
    display_settings_backup/    # 运行“备份MOD”后生成
```

运行后的备份和记录会写到：

```text
L4N_Go_Win32_Portable/.l4n_auto_backup/
```

不要改名或移动 `resources/`。

## 项目目录分类

```text
cmd/l4nfix/                 # 主程序源码、测试与 rsrc.syso
cmd/fontchange/             # 通用字体工具
cmd/l4nfontchange/           # L4N 字体工具
cmd/makeicon/               # 图标生成工具
internal/                   # 共享内部模块
scripts/                    # 构建、测试、打包和发行包清理脚本
assets/windows/             # app.ico、app.manifest
assets/images/              # 图标参考图片
docs/                       # 使用文档
L4N_Go_Win32_Portable/       # 现用 EXE 与 resources，位置不变
dist/                       # 仅保留最新三版便携 ZIP
archive/                    # 历史工具和资源来源镜像
.gotmp/                     # 本地审查证据、撤回快照和临时文件
```

根目录 `build.ps1`、`package.ps1` 是兼容入口，实现在 `scripts/`。当前使用入口：`L4N_Go_Win32_Portable/L4N_Go_Win32.exe`。详见 [目录整理说明](docs/项目目录整理.md)。

## 构建

生成图标：

```powershell
go run .\cmd\makeicon .\assets\images\001.jpg .\assets\windows\app.ico
rsrc -ico .\assets\windows\app.ico -manifest .\assets\windows\app.manifest -o .\cmd\l4nfix\rsrc.syso
```

测试并构建 GUI exe：

```powershell
.\scripts\test.ps1 -Race
.\build.ps1
```

主程序标题为 `L4N YYYYMMDDHHmm`。`build.ps1` 在构建时按北京时间（Asia/Shanghai）写入年月日时分；重复启动保持同一版本号。直接 `go build` 未注入版本时显示 `L4N 开发版`。构建脚本同时更新主程序与两个字体工具，不生成发布 ZIP。主程序包路径为 `./cmd/l4nfix`。`build.ps1 -OutputPath <路径>` 可指定输出位置。

整理便携版时，将生成的主程序和两个字体工具放在便携目录根部，将运行库、Everything、补丁目录和 txt 说明放入 `resources/`。

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

便携包和补丁资源建议通过 GitHub Release 附件分发。运行 `.\package.ps1` 可构建并生成 `dist/L4N_Go_Win32_Portable_YYYYMMDDHHmm.zip`；逐个校验 ZIP 文件内容，并排除备份、解压缓存。已有同名包时停止，避免覆盖。只打包现有成品可用 `.\package.ps1 -SkipBuild`。打包成功后自动仅保留最新三版；单独清理可运行 `.\scripts\prune-packages.ps1`，预览使用 `-WhatIf`。

## 来源与致谢

本项目中相当一部分资源整理方式和处理方法参考自以下 Bilibili 视频：

https://www.bilibili.com/video/BV1oHoMBbE7m/?spm_id_from=333.337.search-card.all.click&vd_source=7fbb056ce8209fd91e3f904175a597fa

感谢原视频作者对 L4D2、L4N 和 DXVK 配置流程的分享。本项目在此基础上整理为 Go + Win32 便携工具，并补充了自动定位、备份还原、Steam 启动项写入和便携目录管理等自动化流程。

字体切换功能参考自 Bilibili 视频《保姆级求生之路2利用L4N平台修改游戏字体以及进行武器检视》：

https://www.bilibili.com/video/BV1JpCjB9Ewp/?spm_id_from=333.337.search-card.all.click&vd_source=7fbb056ce8209fd91e3f904175a597fa

本工具将视频中的 L4N 字体替换思路整理为按钮操作：检测一键处理生成的 `left4dead2/neko/config.vdf`，并在系统字体与游戏默认字体之间切换。
