package risktrace

const (
	midtermCap = 45  // D/F/W 项封顶，固定
	totalCap   = 100 // 总分封顶，固定
)

// bandOf 将总分映射到档位：0-30 低，31-60 中，61-100 高，边界均包含。
func bandOf(total int) Band {
	switch {
	case total <= 30:
		return BandLow
	case total <= 60:
		return BandMid
	default:
		return BandHigh
	}
}
