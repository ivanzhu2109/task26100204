package risktrace

import (
	"math/rand"
	"testing"
)

// refScore 是测试内按业务规则独立书写的对照计分实现。
func refScore(f Facts, w Weights) int {
	t := 0
	if f.GPA < 2.0 {
		t += w.GPA
	}
	if f.AttendDropPct > 20.0 {
		t += w.Attend
	}
	m := f.MidtermDFW * w.Midterm
	if m > 45 {
		m = 45
	}
	t += m
	if !f.HasAid {
		t += w.NoAid
	}
	if f.FirstGen {
		t += w.FirstGen
	}
	if t > 100 {
		t = 100
	}
	return t
}

func refBand(total int) Band {
	if total <= 30 {
		return BandLow
	}
	if total <= 60 {
		return BandMid
	}
	return BandHigh
}

func randFacts(r *rand.Rand) Facts {
	return Facts{
		GPA:           float64(r.Intn(451)) / 100, // 0.00 - 4.50
		AttendDropPct: float64(r.Intn(101)),
		MidtermDFW:    r.Intn(8),
		HasAid:        r.Intn(2) == 0,
		FirstGen:      r.Intn(2) == 0,
	}
}

func randWeights(r *rand.Rand) Weights {
	return Weights{
		GPA:      r.Intn(51),
		Attend:   r.Intn(51),
		Midterm:  r.Intn(51),
		NoAid:    r.Intn(51),
		FirstGen: r.Intn(51),
	}
}

// TestRandomOpsAgainstReference 在小规模连续更新与改权场景下，
// 将每份报告的全部计数及学生解释与逐人按当时事实重算的结果对照。
func TestRandomOpsAgainstReference(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	s := NewStore()

	type ver struct {
		seq   uint64
		facts Facts
	}
	var seq uint64
	current := map[string]Facts{} // 学生当前事实
	history := map[string][]ver{} // 学生事实版本历史
	var ids []string
	curWeights := DefaultWeights

	expectDist := func(w Weights) ([3]int, [3]int, [3][3]int) {
		var oldD, newD [3]int
		var tr [3][3]int
		for _, f := range current {
			ob := refBand(refScore(f, curWeights))
			nb := refBand(refScore(f, w))
			oldD[ob]++
			newD[nb]++
			tr[ob][nb]++
		}
		return oldD, newD, tr
	}

	var reports []Report
	for op := 0; op < 400; op++ {
		switch r.Intn(3) {
		case 0: // 新增学生
			id := string(rune('A'+len(ids)/26)) + string(rune('a'+len(ids)%26)) + string(rune('0'+len(ids)%10))
			for _, e := range ids { // 保证唯一
				if e == id {
					id += "x"
				}
			}
			f := randFacts(r)
			s.AddStudent(id, f)
			seq++
			ids = append(ids, id)
			current[id] = f
			history[id] = append(history[id], ver{seq, f})
		case 1: // 替换事实
			if len(ids) == 0 {
				continue
			}
			id := ids[r.Intn(len(ids))]
			f := randFacts(r)
			s.ReplaceFacts(id, f)
			seq++
			current[id] = f
			history[id] = append(history[id], ver{seq, f})
		case 2: // 发布权重
			w := randWeights(r)
			oldD, newD, tr := expectDist(w)
			rep := s.Publish(w)
			seq++
			if rep.ID != uint64(len(reports)+1) {
				t.Fatalf("报告 ID 不连续: got %d want %d", rep.ID, len(reports)+1)
			}
			if rep.OldWeights != curWeights || rep.NewWeights != w {
				t.Fatalf("报告权重不符: %+v", rep)
			}
			if rep.Total != len(current) {
				t.Fatalf("报告总数不符: got %d want %d", rep.Total, len(current))
			}
			if rep.OldDist != oldD || rep.NewDist != newD || rep.Transitions != tr {
				t.Fatalf("报告 %d 计数不符:\n got  old=%v new=%v tr=%v\n want old=%v new=%v tr=%v",
					rep.ID, rep.OldDist, rep.NewDist, rep.Transitions, oldD, newD, tr)
			}
			reports = append(reports, rep)
			curWeights = w
		}
	}
	if len(reports) == 0 {
		t.Fatal("未产生任何报告")
	}

	// 对每份报告抽样学生，对照解释（事实、旧新得分与档位）。
	for _, rep := range reports {
		for i := 0; i < 10 && i < len(ids); i++ {
			id := ids[r.Intn(len(ids))]
			ex := s.Explain(rep.ID, id)
			// 对照：该报告 seq 之前最后一个版本
			var want *ver
			for i := range history[id] {
				if history[id][i].seq <= rep.Seq {
					want = &history[id][i]
				}
			}
			if want == nil {
				if ex.InCohort {
					t.Fatalf("报告 %d 学生 %s 应不属于当时人群", rep.ID, id)
				}
				continue
			}
			if !ex.InCohort {
				t.Fatalf("报告 %d 学生 %s 应属于当时人群", rep.ID, id)
			}
			if ex.Facts != want.facts {
				t.Fatalf("报告 %d 学生 %s 事实不符: got %+v want %+v", rep.ID, id, ex.Facts, want.facts)
			}
			os, ns := refScore(want.facts, rep.OldWeights), refScore(want.facts, rep.NewWeights)
			if ex.OldScore != os || ex.NewScore != ns ||
				ex.OldBand != refBand(os) || ex.NewBand != refBand(ns) {
				t.Fatalf("报告 %d 学生 %s 得分不符: got %d/%d want %d/%d",
					rep.ID, id, ex.OldScore, ex.NewScore, os, ns)
			}
		}
	}
}

// TestThresholdsAndCaps 覆盖阈值相等、零权重、两层封顶与档位边界。
func TestThresholdsAndCaps(t *testing.T) {
	w := Weights{GPA: 25, Attend: 20, Midterm: 15, NoAid: 10, FirstGen: 10}

	// 阈值相等不触发：GPA=2.0、下降=20%。
	if got := refScore(Facts{GPA: 2.0, AttendDropPct: 20.0, HasAid: true}, w); got != 0 {
		t.Fatalf("阈值相等不应加分, got %d", got)
	}
	if got := score(Facts{GPA: 2.0, AttendDropPct: 20.0, HasAid: true}, w); got != 0 {
		t.Fatalf("库内计分阈值相等不应加分, got %d", got)
	}
	// 阈值刚好越界触发。
	if got := score(Facts{GPA: 1.99, AttendDropPct: 20.01, HasAid: true}, w); got != 45 {
		t.Fatalf("阈值越界应加分, got %d", got)
	}
	// 零权重：全部事实命中也为 0 分。
	if got := score(Facts{GPA: 0, AttendDropPct: 100, MidtermDFW: 9, FirstGen: true}, Weights{}); got != 0 {
		t.Fatalf("零权重应为 0 分, got %d", got)
	}
	// D/F/W 项封顶 45：10 门 * 15 = 150 -> 45。
	if got := score(Facts{GPA: 4.0, MidtermDFW: 10, HasAid: true}, w); got != 45 {
		t.Fatalf("D/F/W 项应封顶 45, got %d", got)
	}
	// 总分封顶 100：50+50+45+50+50 = 245 -> 100。
	full := Weights{GPA: 50, Attend: 50, Midterm: 50, NoAid: 50, FirstGen: 50}
	if got := score(Facts{GPA: 0, AttendDropPct: 100, MidtermDFW: 5, FirstGen: true}, full); got != 100 {
		t.Fatalf("总分应封顶 100, got %d", got)
	}
	// 档位边界：30 低、31 中、60 中、61 高。
	for _, c := range []struct {
		score int
		band  Band
	}{{0, BandLow}, {30, BandLow}, {31, BandMid}, {60, BandMid}, {61, BandHigh}, {100, BandHigh}} {
		if got := bandOf(c.score); got != c.band {
			t.Fatalf("bandOf(%d) = %v, want %v", c.score, got, c.band)
		}
	}
	// 用单权重构造边界分验证端到端分档。
	mk := func(wg int) Weights { return Weights{GPA: wg} }
	lowGPA := Facts{GPA: 1.0, HasAid: true}
	for _, c := range []struct {
		wg   int
		band Band
	}{{30, BandLow}, {31, BandMid}, {60, BandMid}, {50, BandMid}} {
		if got := bandOf(score(lowGPA, mk(c.wg))); got != c.band {
			t.Fatalf("GPA 权重 %d 分档 = %v, want %v", c.wg, got, c.band)
		}
	}
}

// TestReportImmutability 验证先发布、再改成绩并新增学生、再发布：
// 旧报告及原始事实保持不变，新报告旧分布采用更新后的事实。
func TestReportImmutability(t *testing.T) {
	s := NewStore()
	s.AddStudent("s1", Facts{GPA: 3.5, AttendDropPct: 0, HasAid: true})                   // 默认规则 0 分 low
	s.AddStudent("s2", Facts{GPA: 1.0, AttendDropPct: 50, MidtermDFW: 2, FirstGen: true}) // 25+20+30+10+10=95 high

	w1 := Weights{GPA: 10, Attend: 10, Midterm: 10, NoAid: 10, FirstGen: 10}
	r1 := s.Publish(w1)
	ex1Before := s.Explain(r1.ID, "s2")

	// 改变 s2 成绩并新增 s3。
	s.ReplaceFacts("s2", Facts{GPA: 3.8, AttendDropPct: 0, HasAid: true}) // w1 下 0 分 low
	s.AddStudent("s3", Facts{GPA: 0.5, AttendDropPct: 90, MidtermDFW: 6, FirstGen: true})

	w2 := Weights{GPA: 40, Attend: 40, Midterm: 40, NoAid: 40, FirstGen: 40}
	r2 := s.Publish(w2)

	// 旧报告保持不变。
	got1, ok := s.Report(r1.ID)
	if !ok || got1 != r1 {
		t.Fatalf("旧报告被改写或丢失: %+v", got1)
	}
	// 旧报告中的学生解释仍返回当时的原始事实与得分。
	ex1After := s.Explain(r1.ID, "s2")
	if ex1After != ex1Before {
		t.Fatalf("旧报告学生解释被改写: %+v -> %+v", ex1Before, ex1After)
	}
	if ex1After.Facts.GPA != 1.0 || ex1After.OldScore != 95 || ex1After.OldBand != BandHigh {
		t.Fatalf("旧报告事实/得分不符: %+v", ex1After)
	}
	// 晚于 r1 新增的 s3 不属于 r1 当时人群。
	if ex := s.Explain(r1.ID, "s3"); ex.InCohort {
		t.Fatalf("s3 不应属于报告 %d 当时人群", r1.ID)
	}
	// 新报告总数包含 s3，旧分布采用更新后的事实（s2 在 w1 下已为 low）。
	if r2.Total != 3 {
		t.Fatalf("r2 总数应为 3, got %d", r2.Total)
	}
	if r2.OldDist[BandLow] != 2 || r2.OldDist[BandHigh] != 1 {
		t.Fatalf("r2 旧分布应基于更新后事实: %v", r2.OldDist)
	}
	// s2 在 w1 下为 10+10+20+10+10=60 分 mid，s1 为 0 分 low。
	if r1.NewDist[BandMid] != 1 || r1.NewDist[BandLow] != 1 {
		t.Fatalf("r1 新分布不应被后续操作影响: %v", r1.NewDist)
	}
	// r2 中 s2 的解释使用更新后的事实。
	ex2 := s.Explain(r2.ID, "s2")
	if !ex2.InCohort || ex2.Facts.GPA != 3.8 || ex2.OldScore != 0 {
		t.Fatalf("r2 中 s2 解释不符: %+v", ex2)
	}
	// 权重不变也产生新报告，且 ID 独立。
	r3 := s.Publish(w2)
	if r3.ID == r2.ID || r3.OldWeights != w2 || r3.NewWeights != w2 {
		t.Fatalf("权重不变也应产生独立报告: %+v", r3)
	}
	if r3.Transitions[BandLow][BandLow]+r3.Transitions[BandMid][BandMid]+r3.Transitions[BandHigh][BandHigh] != 3 {
		t.Fatalf("权重不变时迁移应全部留在原档: %v", r3.Transitions)
	}
}

// TestAllReportsQueryable 验证全部历史报告可查，不只保留最近若干份。
func TestAllReportsQueryable(t *testing.T) {
	s := NewStore()
	s.AddStudent("s1", Facts{GPA: 1.0, HasAid: true})
	n := 50
	for i := 0; i < n; i++ {
		s.Publish(Weights{GPA: i % 51})
	}
	if s.ReportCount() != n {
		t.Fatalf("报告数 = %d, want %d", s.ReportCount(), n)
	}
	for id := uint64(1); id <= uint64(n); id++ {
		rep, ok := s.Report(id)
		if !ok || rep.ID != id {
			t.Fatalf("报告 %d 不可查", id)
		}
		ex := s.Explain(id, "s1")
		if !ex.InCohort || ex.NewScore != rep.NewWeights.GPA {
			t.Fatalf("报告 %d 学生解释不符: %+v", id, ex)
		}
	}
	if _, ok := s.Report(uint64(n + 1)); ok {
		t.Fatal("不存在的报告应返回 false")
	}
}
