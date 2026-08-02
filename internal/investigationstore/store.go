// Package investigationstore 实现 Investigation Storage Layer V1：
// 调查请求/计划/任务/证据/记忆/评分配置的统一 JSON 文件存储层。
//
// 设计目标（V1 设计文档）：
//  1. 不引入数据库依赖，纯文件系统存储
//  2. 统一 Store 接口（Save/Get/List/Delete/Exists），业务代码不直接操作文件
//  3. 原子写（temp + fsync + rename），进程崩溃不损坏、状态可恢复
//  4. schema_version 校验 + ID 校验，数据结构稳定
//  5. 单文件锁并发控制（避免锁嵌套/顺序反转）
//  6. 未来可平滑迁移 DuckDB/PostgreSQL（保持接口不变，新增 DuckDB 实现即可）
//
// 目录布局（backend/data/investigation/）：
//
//	requests/         调查请求（每请求一个文件）
//	plans/            调查计划（每计划一个文件）
//	tasks/            调查任务（每任务一个文件）
//	evidence/         证据（evidence/{inv}/{ev}.json 单条文件，避免单文件无限增长）
//	memory/           跨案件知识记忆（memory/address|entity|case/ 分目录）
//	score-profile/    评分权重配置（profiles.json）
//	indexes/          索引（evidence-index.json / memory-index.json）
//	archive/          生命周期归档（超出 active/history 上限的旧记录）
package investigationstore

// Store 是存储层统一接口。业务代码通过该接口读写，
// 后续迁移 DuckDB/PostgreSQL 时保持接口不变、替换实现即可。
type Store[T any] interface {
	// Save 保存记录（key 为记录 ID，原子写）。
	Save(key string, v T) error
	// Get 按 ID 读取记录。
	Get(key string) (T, bool)
	// List 返回全部记录。
	List() []T
	// Delete 按 ID 删除记录。
	Delete(key string) error
	// Exists 判断记录是否存在。
	Exists(key string) bool
}
