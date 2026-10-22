# 应用图标与主题

门户的明暗模式与图标主题分别选择。在“我的账户 → 个人资料 → 偏好设置”选择“跟随系统／浅色／深色”，再选择“全站默认／原始图标／简约”或管理员导入的主题。选择按账号保存；更换浏览器登录同一账号也会读取该选择。简约主题采用几何图形、品牌色和统一留白。

## 管理主题

ANAS 管理员在“管理 → 图标主题”导入 `.anas-theme.json` 文件。需要当前管理员身份和五分钟内认证，超过窗口会在页面内请求验证。导入后所有用户可选，但不会替任何人切换，也不会执行部署。页面用当前授权应用预览图标，管理员可以下载内置主题或已导入的主题包。

导入文件是纯数据：允许本地PNG、主题名称、配色与出处，不接受脚本、任意CSS、SVG或外链图片。包上限32MiB、128张图；源图为512–2048px方形PNG，每张最多2MiB。文字颜色必须满足4.5:1对比度。私有库最多8个导入包，总计32MiB。相同内容再次导入不产生副本；同ID不同内容报冲突，应使用新的主题ID。

主题缺少某个应用时保留原图标；主题不可用时回退全站默认，并在偏好设置提示。主题包不决定用户权限或应用链接。损坏文件保留在私有数据卷中，管理员应离线检查原件，不能把未校验文件当成可用主题。

## 全站默认与 Casdoor

Casdoor 的原生应用图标是全站统一的。门户个人选择不会改变它。修改全站默认时，在源配置设置：

```yaml
application_catalog:
  icon_theme: soft
```

自定义主题先从门户下载主题包，把文件放到workspace配置目录内，例如 `themes/my-theme.anas-theme.json`，再设置：

```yaml
application_catalog:
  icon_theme: custom
  theme_file: themes/my-theme.anas-theme.json
```

用现有 `anas config import SOURCE -w WORKSPACE` 导入源配置，再按常规 plan/apply 流程审阅并部署；不能直接修改受CLI管理的 `workspace/config.yml`。包文件的相对路径以workspace配置目录为准。部署冻结实际PNG，随后不依赖导入原文件或网络。门户没有IAM管理写凭据，也不会直接执行apply。仅由配置冻结、没有导入门户库的全站包需从原workspace保全，门户不提供这类包的下载。

## 图标维护与备份

有网页入口的内置Module各自保存512px图标、官方原件、重绘输入和 `assets/icon-source.json`；统一简约SVG保存在仓库 `internal/apptheme/artwork/`。维护者运行 `node scripts/application-themes.mjs --check` 校验出处、摘要和两套生成包；更新矢量后用 `--render` 重新生成PNG，需要已有Playwright依赖及其Chromium，可通过 `PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH` 指定独立Chrome。运行服务只读取PNG。

导入主题与账号选择位于门户现有私有data卷，随data备份。目录权限0700、主题文件0600；重启会重新读取有效包，会话不随备份恢复。

## 主题包字段

| 字段 | 内容 |
| --- | --- |
| `schema` | 固定 `anas.application-theme/v1` |
| `id` | 小写字母开头，仅小写字母、数字及短横线，最多48字符；自定义全站包不能占用 `original`/`soft` |
| `name` | 按界面语言键保存显示名，至少提供非空 `en`，每种最多160字节；未知语言键拒绝 |
| `icons` | 必填对象：稳定应用名对应PNG的Base64字符串；应用名来自Module的 `applications.name`，未覆盖应用自动用原图 |
| `colors` | 可选 `light`/`dark` 对象，只能覆盖门户固定颜色变量（如 `bg`、`surface`、`text`、`primary`），值为 `#RRGGBB`；不能填写CSS规则 |
| `sources` | 可选出处对象，与图标使用同一应用键；记录HTTPS `url`、原件 `sha256`，可用 `generated` 与 `note` 说明重绘 |

从内置包导出后改用新的ID，可以作为编辑模板。`icons` 不接受图片URL、文件路径或SVG字符串。包中不存在安装列表、业务链接或权限字段；这些仍由各Module和原生配置决定。
