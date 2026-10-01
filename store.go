package risktrace

// DefaultWeights 是初始计分规则：25/20/15/10/10。
var DefaultWeights = Weights{GPA: 25, Attend: 20, Midterm: 15, NoAid: 10, FirstGen: 10}

// 签名：把影响计分的五个分量离散化，用于发布时按组聚合计分。
// bit0 GPA<2.0，bit1 出勤下降>20%，bit2 无助学金，bit3 第一代，bit4+ 为 min(门数,46)。
const sigCountShift = 4

// sigOf 从风险事实提取签名。
func sigOf(f Facts) uint16 {
	var sig uint16
	if f.GPA < 2.0 {
		sig |= 1
	}
	if f.AttendDropPct > 20.0 {
		sig |= 1 << 1
	}
	if !f.HasAid {
		sig |= 1 << 2
	}
	if f.FirstGen {
		sig |= 1 << 3
	}
	c := f.MidtermDFW
	if c > 46 {
		c = 46
	}
	if c < 0 {
		c = 0
	}
	return sig | uint16(c)<<sigCountShift
}

// scoreSig 按签名计分，与 score 完全一致。
func scoreSig(sig uint16, w Weights) int {
	total := 0
	if sig&1 != 0 {
		total += w.GPA
	}
	if sig&(1<<1) != 0 {
		total += w.Attend
	}
	mid := int(sig>>sigCountShift) * w.Midterm
	if mid > midtermCap {
		mid = midtermCap
	}
	total += mid
	if sig&(1<<2) != 0 {
		total += w.NoAid
	}
	if sig&(1<<3) != 0 {
		total += w.FirstGen
	}
	if total > totalCap {
		total = totalCap
	}
	return total
}

// score 按给定权重对风险事实计分。
// 先计算 D/F/W 项并封顶 45，再汇总，总分封顶 100。
func score(f Facts, w Weights) int {
	return scoreSig(sigOf(f), w)
}

// factVersion 记录某名学生某一版本的事实及其生效操作序号。
type factVersion struct {
	seq   uint64
	facts Facts
}

// student 是一名学生的全部历史版本（按 seq 升序）。
type student struct {
	versions []factVersion
}

// Store 保存学生风险事实、当前规则与全部历史报告。
// 所有调用顺序执行，不做并发保护。
type Store struct {
	seq       uint64            // 全局操作序号，每次变更递增
	weights   Weights           // 当前生效权重
	ids       map[string]uint32 // 学生 ID -> 学生记录下标
	students  []student         // 全部学生的版本历史
	sigCounts [752]int32        // 当前各签名人数（752 = 16 * 47）
	total     int               // 当前学生总数
	reports   []Report          // 全部历史报告，下标 = ID-1
}

// NewStore 创建以初始计分规则（25/20/15/10/10）为当前规则的空库。
func NewStore() *Store {
	return &Store{
		weights: DefaultWeights,
		ids:     make(map[string]uint32),
	}
}

// setFacts 写入某名学生的一个新版本事实，并维护签名计数。
func (s *Store) setFacts(id string, f Facts) {
	s.seq++
	sig := sigOf(f)
	if idx, ok := s.ids[id]; ok {
		st := &s.students[idx]
		old := sigOf(st.versions[len(st.versions)-1].facts)
		s.sigCounts[old]--
		st.versions = append(st.versions, factVersion{seq: s.seq, facts: f})
	} else {
		s.ids[id] = uint32(len(s.students))
		s.students = append(s.students, student{
			versions: []factVersion{{seq: s.seq, facts: f}},
		})
		s.total++
	}
	s.sigCounts[sig]++
}

// AddStudent 新增一名学生。
func (s *Store) AddStudent(id string, f Facts) {
	s.setFacts(id, f)
}

// ReplaceFacts 完整替换某名学生的风险事实，不产生报告。
func (s *Store) ReplaceFacts(id string, f Facts) {
	s.setFacts(id, f)
}

// Publish 发布一组新权重，立即生效并产生一份不可变影响报告。
// 新旧两套规则作用于发布当时完全相同的学生集合与风险事实。
func (s *Store) Publish(w Weights) Report {
	s.seq++
	old := s.weights
	rep := Report{
		ID:         uint64(len(s.reports)) + 1,
		Seq:        s.seq,
		OldWeights: old,
		NewWeights: w,
		Total:      s.total,
	}
	for sig := uint16(0); sig < uint16(len(s.sigCounts)); sig++ {
		n := s.sigCounts[sig]
		if n == 0 {
			continue
		}
		ob := bandOf(scoreSig(sig, old))
		nb := bandOf(scoreSig(sig, w))
		rep.OldDist[ob] += int(n)
		rep.NewDist[nb] += int(n)
		rep.Transitions[ob][nb] += int(n)
	}
	s.weights = w
	s.reports = append(s.reports, rep)
	return rep
}

// Report 按 ID 查询历史报告，全部报告永久可查。
func (s *Store) Report(id uint64) (Report, bool) {
	if id == 0 || id > uint64(len(s.reports)) {
		return Report{}, false
	}
	return s.reports[id-1], true
}

// ReportCount 返回已产生的报告数。
func (s *Store) ReportCount() int {
	return len(s.reports)
}

// Explain 按报告查询某名学生当时的事实、旧新得分与档位。
// 晚于该报告新增的学生返回 InCohort=false。
func (s *Store) Explain(reportID uint64, studentID string) Explanation {
	rep, ok := s.Report(reportID)
	if !ok {
		return Explanation{}
	}
	idx, ok := s.ids[studentID]
	if !ok {
		return Explanation{InCohort: false}
	}
	vers := s.students[idx].versions
	// 二分查找最后一个 seq <= rep.Seq 的版本。
	lo, hi := 0, len(vers)
	for lo < hi {
		mid := (lo + hi) / 2
		if vers[mid].seq <= rep.Seq {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == 0 {
		return Explanation{InCohort: false}
	}
	f := vers[lo-1].facts
	oldScore := score(f, rep.OldWeights)
	newScore := score(f, rep.NewWeights)
	return Explanation{
		InCohort: true,
		Facts:    f,
		OldScore: oldScore,
		NewScore: newScore,
		OldBand:  bandOf(oldScore),
		NewBand:  bandOf(newScore),
	}
}
