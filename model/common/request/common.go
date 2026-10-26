package request

// 列表接口的分页兜底值：缺省 20 条、单页上限 500 条（租户下拉一次取 100 条）。
const (
	DefaultPageSize = 20
	MaxPageSize     = 500
)

type PageInfo struct {
	PageIndex int    `json:"pageIndex" form:"pageIndex"` //当前页面索引
	PageSize  int    `json:"pageSize" form:"pageSize"`   // 每页大小
	Keyword   string `json:"keyWord" form:"keyWord"`     //关键字
}

// Normalize 规范化分页参数（API 入口调用，结果同时用于回显 PageResult）：
// 页大小缺省/非正取 defaultSize、上限 maxSize；页码非正取 1。
//
// 缺省 pageSize 不能直接透传：GORM 的 Limit(0) 是 LIMIT 0，会把列表查成空集。
func (p *PageInfo) Normalize(defaultSize, maxSize int) {
	if p.PageSize <= 0 {
		p.PageSize = defaultSize
	}
	if maxSize > 0 && p.PageSize > maxSize {
		p.PageSize = maxSize
	}
	if p.PageIndex <= 0 {
		p.PageIndex = 1
	}
}
