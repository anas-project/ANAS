# 文档站点

文档使用 VitePress 构建。中文位于站点根路径，英文镜像位于 `/en/`。

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

该目录可以交给任意静态 HTTP 服务器。GitHub Actions 会在每个 Pull Request 和每次推送到 `master` 时构建；`master` 上的成功构建会发布到 GitHub Pages，Pull Request 只做构建校验。也可以通过 `workflow_dispatch` 手工重跑。

`docs/` 中除构建配置和生成目录外的内容都会参与公开站点编译。导航不是发布边界：不要在本目录保存真实 Secret、测试主机地址、SSH 命令或内部事件记录。单次测试证据应使用受控的 Issue、CI artifact 或外部私有系统；具有长期价值的结论应改写为不含敏感信息的指南、参考或设计文档。
