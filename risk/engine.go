package risk

// 计分常量：判断阈值、D/F/W 项上限、总分上限与分档边界始终固定。
const (
	gpaThreshold        = 2.0
	attendanceThreshold = 20.0
	dfwCap              = 45
	totalCap            = 100
	midBoundary         = 30 // 0-30 低风险
	highBoundary        = 60 // 31-60 中风险，61-100 高风险
)

// defaultWeights 是未发布任何规则前的初始计分权重。
var defaultWeights = Weights{GPA: 25, Attendance: 20, MidtermDFW: 15, NoAid: 10, FirstGen: 10}

// 签名维度：GPA 是否低于阈值、出勤下降是否超过阈值、D/F/W 门数（封顶 45）、
// 是否无助学金、是否第一代大学生。得分只依赖这五个特征，与具体 GPA/出勤数值无关。
const (
	sigDFWSpan   = dfwCap + 1
	sigFlagCombinations = 16
	sigSpace     = sigFlagCombinations * sigDFWSpan // 736
)

// signature 从风险事实提取计分特征，编码为 0..735 的整数。
func signature(f Facts) int {
	sig := 0
	if f.GPA < gpaThreshold {
		sig |= 1
	}
	if f.AttendanceDropPct > attendanceThreshold {
		sig |= 2
	}
	if !f.HasAid {
		sig |= 4
	}
	if f.FirstGen {
		sig |= 8
	}
	dfw := f.MidtermDFW
	if dfw > dfwCap {
		dfw = dfwCap
	}
	return sig*sigDFWSpan + dfw
}

// score 按权重对签名计分：先算 D/F/W 项并封顶 45，再汇总并封顶 100。
func score(w Weights, sig int) int {
	dfw := sig % sigDFWSpan
	flags := sig / sigDFWSpan
	total := 0
	if flags&1 != 0 {
		total += w.GPA
	}
	if flags&2 != 0 {
		total += w.Attendance
	}
	dfwTerm := dfw * w.MidtermDFW
	if dfwTerm > dfwCap {
		dfwTerm = dfwCap
	}
	total += dfwTerm
	if flags&4 != 0 {
		total += w.NoAid
	}
	if flags&8 != 0 {
		total += w.FirstGen
	}
	if total > totalCap {
		total = totalCap
	}
	return total
}

// tierOf 按固定边界分档，边界值包含在较低档位。
func tierOf(score int) Tier {
	if score <= midBoundary {
		return TierLow
	}
	if score <= highBoundary {
		return TierMid
	}
	return TierHigh
}

// versionedFacts 是一条带全局序号的事实版本。
type versionedFacts struct {
	seq   uint64
	facts Facts
}

// studentHistory 是一名学生的事实变更日志，按 seq 递增追加。
type studentHistory struct {
	versions []versionedFacts
}

// Engine 是改权影响追溯库的主入口，所有调用顺序执行。
type Engine struct {
	students  map[string]*studentHistory
	sigCounts [sigSpace]int32 // 当前学生集合在各签名上的人数
	seq       uint64          // 全局事实版本序号
	weights   Weights         // 当前生效的权重
	reports   []Report
	reportSeq []uint64 // 每份报告发布时的事实版本序号
}

// NewEngine 创建一个空引擎，初始规则为默认权重。
func NewEngine() *Engine {
	return &Engine{
		students: make(map[string]*studentHistory),
		weights:  defaultWeights,
	}
}

// AddStudent 新增一名学生及其风险事实。
func (e *Engine) AddStudent(id string, f Facts) {
	e.seq++
	h := &studentHistory{versions: []versionedFacts{{seq: e.seq, facts: f}}}
	e.students[id] = h
	e.sigCounts[signature(f)]++
}

// ReplaceFacts 完整替换某名学生的风险事实，不产生报告。
func (e *Engine) ReplaceFacts(id string, f Facts) {
	e.seq++
	h := e.students[id]
	old := h.versions[len(h.versions)-1].facts
	h.versions = append(h.versions, versionedFacts{seq: e.seq, facts: f})
	e.sigCounts[signature(old)]--
	e.sigCounts[signature(f)]++
}

// Publish 发布一组新权重，立即生效并产生一份不可变影响报告，返回报告 ID。
// 新旧两套规则作用于发布当时完全相同的学生集合与风险事实。
func (e *Engine) Publish(w Weights) int {
	var rep Report
	rep.OldWeights = e.weights
	rep.NewWeights = w
	for sig := 0; sig < sigSpace; sig++ {
		n := int(e.sigCounts[sig])
		if n == 0 {
			continue
		}
		rep.TotalStudents += n
		oldTier := tierOf(score(e.weights, sig))
		newTier := tierOf(score(w, sig))
		rep.OldCounts[oldTier] += n
		rep.NewCounts[newTier] += n
		rep.Transitions[oldTier][newTier] += n
	}
	e.weights = w
	rep.ID = len(e.reports) + 1
	e.reports = append(e.reports, rep)
	e.reportSeq = append(e.reportSeq, e.seq)
	return rep.ID
}

// Report 按 ID 查询历史报告。
func (e *Engine) Report(id int) Report {
	return e.reports[id-1]
}

// StudentInReport 按报告查询某名学生当时的风险事实与新旧得分、档位。
// 晚于该报告新增的学生返回 InPopulation=false。
func (e *Engine) StudentInReport(reportID int, studentID string) StudentRecord {
	rep := e.reports[reportID-1]
	seq := e.reportSeq[reportID-1]
	h, ok := e.students[studentID]
	if !ok {
		return StudentRecord{}
	}
	// 二分查找 seq 不大于报告序号的最后一个事实版本。
	lo, hi := 0, len(h.versions)
	for lo < hi {
		mid := (lo + hi) / 2
		if h.versions[mid].seq <= seq {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == 0 {
		return StudentRecord{}
	}
	facts := h.versions[lo-1].facts
	sig := signature(facts)
	oldScore := score(rep.OldWeights, sig)
	newScore := score(rep.NewWeights, sig)
	return StudentRecord{
		InPopulation: true,
		Facts:        facts,
		OldScore:     oldScore,
		NewScore:     newScore,
		OldTier:      tierOf(oldScore),
		NewTier:      tierOf(newScore),
	}
}
