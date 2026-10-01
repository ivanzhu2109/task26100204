package risktrace

// Facts 是某名学生在某一时刻的风险事实。
type Facts struct {
	GPA           float64 // 平均绩点
	AttendDropPct float64 // 出勤下降百分比
	MidtermDFW    int     // 期中 D/F/W 门数，非负
	HasAid        bool    // 是否有助学金
	FirstGen      bool    // 是否第一代大学生
}

// Weights 是一次发布指定的五个权重，均为 0 至 50 的整数。
type Weights struct {
	GPA      int // GPA 低于 2.0 的加分
	Attend   int // 出勤下降超过 20% 的加分
	Midterm  int // 每门期中 D/F/W 的加分（该项总分封顶 45）
	NoAid    int // 无助学金的加分
	FirstGen int // 第一代大学生的加分
}

// Band 是风险档位。
type Band int

const (
	BandLow  Band = iota // 0-30
	BandMid              // 31-60
	BandHigh             // 61-100
)

func (b Band) String() string {
	switch b {
	case BandLow:
		return "low"
	case BandMid:
		return "mid"
	case BandHigh:
		return "high"
	}
	return "unknown"
}

// Report 是一次权重发布产生的不可变影响报告。
type Report struct {
	ID          uint64    // 报告唯一标识，从 1 开始递增
	Seq         uint64    // 产生报告时的操作序号
	OldWeights  Weights   // 发布前实际生效的权重
	NewWeights  Weights   // 本次发布的权重
	Total       int       // 当时学生总数
	OldDist     [3]int    // 旧规则下低/中/高各档人数
	NewDist     [3]int    // 新规则下低/中/高各档人数
	Transitions [3][3]int // 旧档位 -> 新档位 的迁移人数
}

// Explanation 是按报告查询某名学生得到的当时解释。
type Explanation struct {
	InCohort bool  // 是否属于该报告当时的人群
	Facts    Facts // 当时的风险事实（InCohort 为 false 时为零值）
	OldScore int   // 旧规则得分
	NewScore int   // 新规则得分
	OldBand  Band  // 旧规则档位
	NewBand  Band  // 新规则档位
}
