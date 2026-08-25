# 参考文档

参考文档定义字段、格式和稳定接口，适合查找，不代替任务指南。

- [配置结构](configuration.md)：`config.yml` 的结构化字段和环境变量映射。
- [Module 目录](modules.md)：稳定与实验性 Module 的能力边界。
- [Module 环境变量](module-environment-variables.md)：Runner 与 Module 之间的变量契约。
- [Module 专属命令](module-commands.md)：Module 命令声明、只读发现和安全边界。
- [CLI JSON 契约](contracts/)：机器可读输出、退出码和命令载荷。
- Module Contract 参考：[relational_database](module-contracts/relational_database.md)、[object_storage](module-contracts/object_storage.md)、[identity](module-contracts/identity.md)、[certificate](module-contracts/certificate.md)、[compute](module-contracts/compute.md)，均由 `contracts/<name>/` 生成。

操作步骤请从[使用指南](/guide/)进入；设计原因请查看[架构与设计](/architecture/)。
