// Package risktrace 提供学业风险规则改权影响追溯能力。
// 学生风险事实、规则与历史报告全部保存在进程内存中，由上层直接调用。
package risk

// Facts 是一名学生的风险事实。
type Facts struct {
	GPA               float64 // 平均绩点
	AttendanceDropPct float64 // 出勤下降百分比
	MidtermDFW        int     // 期中 D/F/W 门数，非负
	HasAid            bool    // 是否有助学金
	FirstGen          bool    // 是否第一代大学生
}

// Weights 是一次发布指定的五个计分权重，均为 0 至 50 的整数。
type Weights struct {
	GPA        int // GPA 低于 2.0 的加分
	Attendance int // 出勤下降超过 20% 的加分
	MidtermDFW int // 每门期中 D/F/W 的加分（该项封顶 45）
	NoAid      int // 无助学金的加分
	FirstGen   int // 第一代大学生的加分
}

// Tier 是风险档位。
type Tier int

const (
	TierLow  Tier = iota // 0-30 分
	TierMid              // 31-60 分
	TierHigh             // 61-100 分
)

func (t Tier) String() string {
	switch t {
	case TierLow:
		return "low"
	case TierMid:
		return "mid"
	case TierHigh:
		return "high"
	}
	return "unknown"
}

// Report 是一份不可变的影响报告。
type Report struct {
	ID            int       // 报告标识，从 1 开始递增
	OldWeights    Weights   // 发布前实际生效的权重
	NewWeights    Weights   // 本次发布的权重
	TotalStudents int       // 发布当时的学生总数
	OldCounts     [3]int    // 旧规则下低/中/高各档人数
	NewCounts     [3]int    // 新规则下低/中/高各档人数
	Transitions   [3][3]int // 旧档位 -> 新档位的迁移人数
}

// StudentRecord 是按报告查询某名学生时返回的解释。
type StudentRecord struct {
	InPopulation bool   // 是否属于该报告当时的人群
	Facts        Facts  // 该报告当时的学生风险事实
	OldScore     int    // 旧规则得分
	NewScore     int    // 新规则得分
	OldTier      Tier   // 旧规则档位
	NewTier      Tier   // 新规则档位
}
