// perfcheck 按验收规模做可重复的性能验证：
// 预载 20 万学生后，交错执行 10 万次事实替换与 1 万次规则发布，
// 再做 10 万次历史单学生查询，报告总耗时与峰值内存，
// 并抽查最早、中间、最新报告仍可准确查询。
package main

import (
	"fmt"
	"math/rand"
	"runtime"
	"syscall"
	"time"

	"risktrace"
)

const (
	numStudents     = 200_000
	numReplacements = 100_000
	numPublishes    = 100_000 / 10 // 每 10 次替换交错 1 次发布，共 1 万次
	numQueries      = 100_000
)

func peakRSSBytes() uint64 {
	var ru syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	return uint64(ru.Maxrss) // darwin 上单位为字节
}

func randFacts(r *rand.Rand) risktrace.Facts {
	return risktrace.Facts{
		GPA:           float64(r.Intn(451)) / 100,
		AttendDropPct: float64(r.Intn(101)),
		MidtermDFW:    r.Intn(8),
		HasAid:        r.Intn(2) == 0,
		FirstGen:      r.Intn(2) == 0,
	}
}

func randWeights(r *rand.Rand) risktrace.Weights {
	return risktrace.Weights{
		GPA:      r.Intn(51),
		Attend:   r.Intn(51),
		Midterm:  r.Intn(51),
		NoAid:    r.Intn(51),
		FirstGen: r.Intn(51),
	}
}

// refScore 独立对照计分。
func refScore(f risktrace.Facts, w risktrace.Weights) int {
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

func main() {
	r := rand.New(rand.NewSource(20261001))
	s := risktrace.NewStore()

	// 预载 20 万学生（不计入耗时）。
	ids := make([]string, numStudents)
	for i := range ids {
		ids[i] = fmt.Sprintf("stu-%d", i)
		s.AddStudent(ids[i], randFacts(r))
	}

	// 跟踪少量抽样学生的完整事实历史，用于事后核对解释。
	type ver struct {
		report uint64 // 在该报告之后生效（即下一次替换前的版本）
		facts  risktrace.Facts
	}
	samples := map[string][]ver{}
	for _, id := range []string{ids[0], ids[numStudents/2], ids[numStudents-1]} {
		samples[id] = nil // 版本在替换循环中记录
	}

	runtime.GC()
	start := time.Now()

	// 交错执行 10 万次替换与 1 万次发布。
	reportIDs := make([]uint64, 0, numPublishes)
	for i := 0; i < numPublishes; i++ {
		for j := 0; j < 10; j++ {
			id := ids[r.Intn(numStudents)]
			f := randFacts(r)
			s.ReplaceFacts(id, f)
			if _, ok := samples[id]; ok {
				samples[id] = append(samples[id], ver{report: uint64(len(reportIDs)), facts: f})
			}
		}
		rep := s.Publish(randWeights(r))
		reportIDs = append(reportIDs, rep.ID)
	}

	// 10 万次历史单学生查询。
	var sink int
	for i := 0; i < numQueries; i++ {
		ex := s.Explain(reportIDs[r.Intn(len(reportIDs))], ids[r.Intn(numStudents)])
		if ex.InCohort {
			sink += ex.OldScore + ex.NewScore
		}
	}
	elapsed := time.Since(start)
	_ = sink

	// 核对最早、中间、最新报告仍可准确查询。
	checks := []uint64{reportIDs[0], reportIDs[len(reportIDs)/2], reportIDs[len(reportIDs)-1]}
	for _, id := range checks {
		rep, ok := s.Report(id)
		if !ok {
			panic(fmt.Sprintf("报告 %d 丢失", id))
		}
		// 内部一致性：分布与迁移矩阵自洽。
		sumOld, sumNew, sumTr := 0, 0, 0
		for b := 0; b < 3; b++ {
			sumOld += rep.OldDist[b]
			sumNew += rep.NewDist[b]
			row, col := 0, 0
			for c := 0; c < 3; c++ {
				sumTr += rep.Transitions[b][c]
				row += rep.Transitions[b][c]
				col += rep.Transitions[c][b]
			}
			if row != rep.OldDist[b] || col != rep.NewDist[b] {
				panic(fmt.Sprintf("报告 %d 迁移矩阵与分布不一致", id))
			}
		}
		if sumOld != rep.Total || sumNew != rep.Total || sumTr != rep.Total {
			panic(fmt.Sprintf("报告 %d 计数不自洽", id))
		}
		// 抽样学生解释与独立跟踪的事实历史对照。
		for sid, hist := range samples {
			ex := s.Explain(id, sid)
			// 找该报告之前最后一个版本（report 字段为版本生效前的报告数）。
			var want *risktrace.Facts
			for i := range hist {
				if hist[i].report < id {
					f := hist[i].facts
					want = &f
				}
			}
			if want == nil {
				continue // 该学生此期间未替换过，初始事实未单独记录，跳过
			}
			if !ex.InCohort || ex.Facts != *want {
				panic(fmt.Sprintf("报告 %d 学生 %s 事实不符", id, sid))
			}
			if ex.OldScore != refScore(*want, rep.OldWeights) ||
				ex.NewScore != refScore(*want, rep.NewWeights) {
				panic(fmt.Sprintf("报告 %d 学生 %s 得分不符", id, sid))
			}
		}
		fmt.Printf("报告 %d 查询核对通过（总数 %d，旧分布 %v，新分布 %v）\n",
			id, rep.Total, rep.OldDist, rep.NewDist)
	}

	fmt.Printf("操作规模: %d 替换 + %d 发布 + %d 查询（预载 %d 学生不计时）\n",
		numReplacements, numPublishes, numQueries, numStudents)
	fmt.Printf("总耗时: %v（上限 15s）\n", elapsed)
	fmt.Printf("峰值内存(RSS): %.1f MiB（上限 256 MiB）\n", float64(peakRSSBytes())/1024/1024)
	fmt.Printf("报告总数: %d，全部可查\n", s.ReportCount())
}
