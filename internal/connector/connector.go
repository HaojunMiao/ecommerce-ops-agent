// Package connector 定义知识源接入协议。
package connector

import "context"

// Document 是所有知识源统一输出的标准文档。
// Connector 只负责把外部内容转换到这一层，后续切片、向量化和索引
// 都由知识库服务统一处理。
type Document struct {
	// SourceURI 是文档在知识源中的稳定位置，用于识别同一份文档。
	SourceURI string
	// Title 用于管理端展示和后续检索结果引用。
	Title string
	// Content 保留文档的完整原始文本。
	Content string
	// Checksum 用于判断同一路径下的文档内容是否发生变化。
	Checksum string
}

// Connector 屏蔽不同知识源的读取方式。
// 例如本地 Markdown、对象存储和在线文档都可以实现该接口。
type Connector interface {
	Scan(ctx context.Context) ([]Document, error)
}
