package risk

import (
	"math/rand"
	"testing"
)

// refScore 是测试内的独立对照计分实现，刻意不复用库内函数。
func refScore(w Weights, f Facts) int {
	total := 0
	if f.GPA < 2.0 {
		total += w.GPA
	}
	if f.AttendanceDropPct > 20.0 {
		total += w.Attendance
	}
	dfw := f.MidtermDFW * w.MidtermDFW
	if dfw > 45 {
		dfw = 45
	}
	total += dfw
	if !f.HasAid {
		total += w.NoAid
	}
	if f.FirstGen {
		total += w.FirstGen
	}
	if total > 100 {
		total = 100
	}
	return total
}

func refTier(s int) Tier {
	switch {
	case s <= 30:
		return TierLow
	case s <= 60:
		return TierMid
	default:
		return TierHigh
	}
}

// refReport 用逐人重算的方式生成对照报告。
func refReport(id int, oldW, newW Weights, pop map[string]Facts) Report {
	rep := Report{ID: id, OldWeights: oldW, NewWeights: newW, TotalStudents: len(pop)}
	for _, f := range pop {
		ot := refTier(refScore(oldW, f))
		nt := refTier(refScore(newW, f))
		rep.OldCounts[ot]++
		rep.NewCounts[nt]++
		rep.Transitions[ot][nt]++
	}
	return rep
}

func checkReport(t *testing.T, got, want Report) {
	t.Helper()
	if got != want {
		t.Fatalf("报告不一致:\n got=%+v\nwant=%+v", got, want)
	}
}

// TestScoringEdgeCases 覆盖阈值相等、零权重、两层封顶与档位边界。
func TestScoringEdgeCases(t *testing.T) {
	w := Weights{GPA: 25, Attendance: 20, MidtermDFW: 15, NoAid: 10, FirstGen: 10}
	cases := []struct {
		name  string
		f     Facts
		want  int
	}{
		{"GPA恰好2.0不加分", Facts{GPA: 2.0, HasAid: true}, 0},
		{"GPA略低于2.0加分", Facts{GPA: 1.99, HasAid: true}, 25},
		{"出勤下降恰好20不加分", Facts{GPA: 3.0, AttendanceDropPct: 20.0, HasAid: true}, 0},
		{"出勤下降略超20加分", Facts{GPA: 3.0, AttendanceDropPct: 20.01, HasAid: true}, 20},
		{"DFW三门恰好45", Facts{GPA: 4.0, MidtermDFW: 3, HasAid: true}, 45},
		{"DFW四门封顶45", Facts{GPA: 4.0, MidtermDFW: 4, HasAid: true}, 45},
		{"DFW十门仍封顶45", Facts{GPA: 4.0, MidtermDFW: 10, HasAid: true}, 45},
		{"总分封顶100", Facts{GPA: 1.0, AttendanceDropPct: 50, MidtermDFW: 10, HasAid: false, FirstGen: true}, 100},
		{"边界30分低风险", Facts{GPA: 3.0, AttendanceDropPct: 25, MidtermDFW: 0, HasAid: false, FirstGen: false}, 30},
	}
	for _, c := range cases {
		if got := score(w, signature(c.f)); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
	// 档位边界：30 低、31 中、60 中、61 高。
	bounds := map[int]Tier{0: TierLow, 30: TierLow, 31: TierMid, 60: TierMid, 61: TierHigh, 100: TierHigh}
	for s, want := range bounds {
		if got := tierOf(s); got != want {
			t.Errorf("tierOf(%d)=%v, want %v", s, got, want)
		}
	}
	// 零权重：任何事实都得 0 分低风险。
	zero := Weights{}
	extreme := Facts{GPA: 0, AttendanceDropPct: 100, MidtermDFW: 99, HasAid: false, FirstGen: true}
	if got := score(zero, signature(extreme)); got != 0 {
		t.Errorf("零权重得分 got %d, want 0", got)
	}
	// DFW 权重为零时门数不影响得分。
	if got := score(Weights{MidtermDFW: 0}, signature(Facts{MidtermDFW: 7, HasAid: true})); got != 0 {
		t.Errorf("DFW零权重 got %d, want 0", got)
	}
}

// TestContinuousUpdates 小规模连续更新与改权，每份报告与逐人重算对照。
func TestContinuousUpdates(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	e := NewEngine()
	pop := map[string]Facts{}
	snaps := map[int]map[string]Facts{} // 每份报告发布时的人群快照
	weightSeq := []Weights{defaultWeights}

	randFacts := func() Facts {
		return Facts{
			GPA:               float64(rng.Intn(451)) / 100,
			AttendanceDropPct: float64(rng.Intn(601)) / 10,
			MidtermDFW:        rng.Intn(8),
			HasAid:            rng.Intn(2) == 0,
			FirstGen:          rng.Intn(2) == 0,
		}
	}
	randWeights := func() Weights {
		return Weights{
			GPA: rng.Intn(51), Attendance: rng.Intn(51), MidtermDFW: rng.Intn(51),
			NoAid: rng.Intn(51), FirstGen: rng.Intn(51),
		}
	}

	ids := []string{}
	for i := 0; i < 60; i++ {
		id := string(rune('A'+i/26)) + string(rune('a'+i%26)) + string(rune('0'+i%10))
		ids = append(ids, id)
		f := randFacts()
		e.AddStudent(id, f)
		pop[id] = f
	}

	for round := 0; round < 25; round++ {
		// 交错执行事实替换与新增。
		for k := 0; k < 5; k++ {
			id := ids[rng.Intn(len(ids))]
			f := randFacts()
			e.ReplaceFacts(id, f)
			pop[id] = f
		}
		if round%3 == 0 {
			id := ids[0] + "x" + string(rune('a'+round))
			ids = append(ids, id)
			f := randFacts()
			e.AddStudent(id, f)
			pop[id] = f
		}
		w := randWeights()
		if round%7 == 0 {
			w = weightSeq[len(weightSeq)-1] // 权重不变也要产生报告
		}
		repID := e.Publish(w)
		weightSeq = append(weightSeq, w)

		// 对照整份报告。
		want := refReport(repID, weightSeq[len(weightSeq)-2], w, pop)
		checkReport(t, e.Report(repID), want)

		// 记录快照供后续学生解释对照。
		snap := make(map[string]Facts, len(pop))
		for id, f := range pop {
			snap[id] = f
		}
		snaps[repID] = snap
	}

	// 全部历史报告的学生解释与快照逐人重算对照。
	for repID, snap := range snaps {
		rep := e.Report(repID)
		for id, f := range snap {
			got := e.StudentInReport(repID, id)
			if !got.InPopulation {
				t.Fatalf("报告%d 学生%s 应在人群中", repID, id)
			}
			if got.Facts != f {
				t.Fatalf("报告%d 学生%s 事实不一致: got %+v want %+v", repID, id, got.Facts, f)
			}
			if got.OldScore != refScore(rep.OldWeights, f) || got.NewScore != refScore(rep.NewWeights, f) ||
				got.OldTier != refTier(got.OldScore) || got.NewTier != refTier(got.NewScore) {
				t.Fatalf("报告%d 学生%s 解释不一致: %+v", repID, id, got)
			}
		}
	}
}

// TestImmutability 验证发布后改成绩、新增学生不影响旧报告，新报告用更新后的事实。
func TestImmutability(t *testing.T) {
	e := NewEngine()
	e.AddStudent("s1", Facts{GPA: 3.5, HasAid: true})                       // 默认规则 0 分
	e.AddStudent("s2", Facts{GPA: 1.5, HasAid: true})                       // 默认规则 25 分
	rep1 := e.Publish(Weights{GPA: 50, Attendance: 0, MidtermDFW: 0, NoAid: 0, FirstGen: 0})
	r1 := e.Report(rep1)
	if r1.OldCounts != [3]int{2, 0, 0} || r1.NewCounts != [3]int{1, 1, 0} {
		t.Fatalf("报告1分布错误: %+v", r1)
	}
	s1Before := e.StudentInReport(rep1, "s1")

	// 发布后改变成绩并新增学生。
	e.ReplaceFacts("s1", Facts{GPA: 1.0, HasAid: true})
	e.AddStudent("s3", Facts{GPA: 1.2, HasAid: false, FirstGen: true})
	rep2 := e.Publish(Weights{GPA: 10, Attendance: 10, MidtermDFW: 10, NoAid: 10, FirstGen: 10})

	// 旧报告及原始事实保持不变。
	if got := e.Report(rep1); got != r1 {
		t.Fatalf("报告1被改写:\n got=%+v\nwant=%+v", got, r1)
	}
	if got := e.StudentInReport(rep1, "s1"); got != s1Before {
		t.Fatalf("报告1中s1解释被改写:\n got=%+v\nwant=%+v", got, s1Before)
	}
	if got := e.StudentInReport(rep1, "s1"); got.Facts.GPA != 3.5 {
		t.Fatalf("报告1中s1的GPA应为当时的3.5, got %v", got.Facts.GPA)
	}
	// 晚于报告1新增的 s3 不属于当时人群。
	if got := e.StudentInReport(rep1, "s3"); got.InPopulation {
		t.Fatalf("s3 不应属于报告1的人群: %+v", got)
	}

	// 新报告的旧分布必须采用更新后的事实（s1 已改为 GPA 1.0），共 3 名学生。
	r2 := e.Report(rep2)
	if r2.TotalStudents != 3 {
		t.Fatalf("报告2总人数 got %d, want 3", r2.TotalStudents)
	}
	// 旧权重 {50,0,0,0,0}：s1(1.0)->50, s2(1.5)->50, s3(1.2)->50，全部中风险。
	if r2.OldCounts != [3]int{0, 3, 0} {
		t.Fatalf("报告2旧分布未采用更新后事实: %+v", r2.OldCounts)
	}
	// 新权重各 10：s1=10, s2=10, s3=10+10+10=30，全部低风险。
	if r2.NewCounts != [3]int{3, 0, 0} {
		t.Fatalf("报告2新分布错误: %+v", r2.NewCounts)
	}
	if r2.Transitions != [3][3]int{{0, 0, 0}, {3, 0, 0}, {0, 0, 0}} {
		t.Fatalf("报告2迁移矩阵错误: %+v", r2.Transitions)
	}
	// s3 在报告2中可查，且事实是新增时的版本。
	got := e.StudentInReport(rep2, "s3")
	if !got.InPopulation || got.OldScore != 50 || got.NewScore != 30 {
		t.Fatalf("报告2中s3解释错误: %+v", got)
	}
}

// TestSameScoreReplaceKeepsFacts 替换前后得分相同也不能丢失历史事实。
func TestSameScoreReplaceKeepsFacts(t *testing.T) {
	e := NewEngine()
	e.AddStudent("s1", Facts{GPA: 1.5, AttendanceDropPct: 0, HasAid: true})
	rep1 := e.Publish(Weights{GPA: 30, Attendance: 0, MidtermDFW: 0, NoAid: 0, FirstGen: 0})
	// GPA 1.5->1.9 在 {30,0,0,0,0} 下得分都是 30，但事实不同。
	e.ReplaceFacts("s1", Facts{GPA: 1.9, AttendanceDropPct: 0, HasAid: true})
	got := e.StudentInReport(rep1, "s1")
	if got.Facts.GPA != 1.5 {
		t.Fatalf("历史事实丢失: got GPA %v, want 1.5", got.Facts.GPA)
	}
}

// TestUnknownStudent 查询不存在的学生。
func TestUnknownStudent(t *testing.T) {
	e := NewEngine()
	e.AddStudent("s1", Facts{GPA: 3.0, HasAid: true})
	rep := e.Publish(Weights{GPA: 1, Attendance: 1, MidtermDFW: 1, NoAid: 1, FirstGen: 1})
	if got := e.StudentInReport(rep, "nobody"); got.InPopulation {
		t.Fatalf("不存在的学生应返回 InPopulation=false")
	}
}
