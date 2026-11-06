# SPEC 归档

读取时机：用户请求归档，或项目（`CLAUDE.md` / 当前 SPEC）明确规定本次交付需要归档时。`archive/` 目录存在本身不是触发条件。在状态同步之后、暂存之前执行。

## 执行

1. 项目 `CLAUDE.md` 或当前 SPEC 已声明归档命令时，优先使用项目命令。
2. 否则用本 skill 自带脚本，先 dry-run 查看计划，再正式执行：

   ```bash
   python3 "<ship-skill-dir>/scripts/archive_specs.py" --root "<项目根目录>" --dry-run
   python3 "<ship-skill-dir>/scripts/archive_specs.py" --root "<项目根目录>"
   ```

内置归档器仅在根级 SPEC 数量超过 10 时触发，从 SPEC 元数据判断完成状态，按创建日期降序、ID 升序保留最新 3 个已完成 SPEC，把其余已完成 SPEC 移到 `version/archive/`，并修正本地入链与文档内的相对链接。目标碰撞或写入失败时停止并回滚，活跃需求不动；不修改 Git 暂存区；日常工具只接受新命名，旧项目先升级。

`--force` 绕过数量阈值，仅在用户明确要求或项目规则明确规定时使用。

## 失败处理

脚本缺失、`python3` 不可用、元数据校验失败或执行失败时，记录归档缺口，不手工完成半套移动；继续独立验证，不把失败当作已完成归档。

## 归档后检查

1. `git diff --name-status`，确认移动清单与脚本输出一致。
2. 用一次 `rg -nF -e <文件名 1> -e <文件名 2> …` 搜索所有被移动的文件名，确认每处引用都已指向 `version/archive/` 下的新位置；有遗漏的相对链接就修正。项目有 Markdown link checker 时一并运行。
3. 重跑 `git diff --check`。
4. 把归档数量（或 no-op）写入最终报告。
