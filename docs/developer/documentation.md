# 文档站点

文档使用 VitePress 构建。中文位于站点根路径，英文镜像位于 `/en/`。

新增或修改内容前必须阅读[文档写作标准](documentation-standard.md)。该标准规定目录分类、中英文同步、事实来源、设计状态、链接、安全和提交检查。

## 本地预览

```bash
npm ci
npm run docs:dev
```

## 生产构建

```bash
npm run docs:build
```

静态产物位于：

```text
docs/.vitepress/dist/
```

该目录可以交给任意静态 HTTP 服务器。GitHub Actions 会在每个 Pull Request、每次推送到 `master` 以及 Core 或 Module 发布完成后构建；`master` 上及发布后的成功构建会发布到 GitHub Pages，Pull Request 只做构建校验。也可以通过 `workflow_dispatch` 手工重跑。

## 文档版本策略

站点根路径始终发布持续维护的最新文档，并在首页和每一页的顶部导航显示最新的稳定 Core 版本。版本号来自精确匹配 `vMAJOR.MINOR.PATCH` 的 Git 标签；预发布标签和 Module 标签不会改变该标记。

文档只在跨 Core 主版本时保留快照，不为每个补丁或次版本复制整站。构建会选择每个历史主版本的最后一个稳定标签，例如发布 `v1.0.0` 后，将最后的 `v0.x` 文档保留在 `/versions/0.x/`。历史页面显示固定的版本提示和最新版入口。稳定标签是不可变的，因此历史快照也不在原地修改；对最新版的修正继续进入站点根路径。当前文档仍严格检查死链；历史标签中的既有死链会按原样保留，但不会阻断新版站点发布。

版本选择结果也写入构建产物根目录的 `versions.json`，供重定向、站点检查或其他发布工具读取。运行以下命令验证标签筛选规则并构建包含历史快照的完整站点：

```bash
npm run docs:test-versions
npm run docs:build
```

站点默认公开地址为 `https://anas-project.github.io`。如果把版本化产物发布到其他域名，请在构建时设置 `DOCS_SITE_ORIGIN`；`DOCS_BASE` 继续控制该域名下的站点根路径。

`docs/` 中除构建配置和生成目录外的内容都会参与公开站点编译。导航不是发布边界：不要在本目录保存真实 Secret、测试主机地址、SSH 命令或内部事件记录。单次测试证据应使用受控的 Issue、CI artifact 或外部私有系统；具有长期价值的结论应改写为不含敏感信息的指南、参考或设计文档。
