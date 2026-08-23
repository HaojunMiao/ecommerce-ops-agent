package platform

// Agent控制面的一些配置
// 负责“Agent应该怎么运行”的配置
// prompt、tool、skill等都会在这里管理

// Platform 控制面的入口
// 后续逐步添加相应的内容
type Platform struct {
}

// New 创建新的控制面
func New() *Platform {
	return &Platform{}
}
