// scalecheck 按验收规模做可重复验证：
// 预载 20 万学生后，交错执行 10 万次事实替换与 1 万次规则发布（保留全部报告），
// 再做 10 万次历史单学生查询，报告总耗时与峰值内存，
// 并对最早、中间、最新三份报告做独立逐人重算对照。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"syscall"
	"time"

	risk "risktrace/risk"
)

const (
	numStudents    = 200_000
	numReplaces    = 100_000
	numPublishes   = 10_000
	numQueries     = 100_000
	replacePerPub  = numReplaces / numPublishes // 10
	timeBudgetSecs = 15
	memBudgetMiB   = 256
)

// 独立对照实现，刻意不复用库内代码。
func refScore(w risk.Weights, f risk.Facts) int {
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

func refTier(s int) int {
	if s <= 30 {
		return 0
	}
	if s <= 60 {
		return 1
	}
	return 2
}

func randFacts(rng *rand.Rand) risk.Facts {
	return risk.Facts{
		GPA:               float64(rng.Intn(451)) / 100,
		AttendanceDropPct: float64(rng.Intn(601)) / 10,
		MidtermDFW:        rng.Intn(8),
		HasAid:            rng.Intn(2) == 0,
		FirstGen:          rng.Intn(2) == 0,
	}
}

func randWeights(rng *rand.Rand) risk.Weights {
	return risk.Weights{
		GPA: rng.Intn(51), Attendance: rng.Intn(51), MidtermDFW: rng.Intn(51),
		NoAid: rng.Intn(51), FirstGen: rng.Intn(51),
	}
}

func peakRSSMiB() float64 {
	var ru syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	return float64(ru.Maxrss) / (1 << 20)
}

func studentID(i int) string { return fmt.Sprintf("stu%07d", i) }

func main() {
	rng := rand.New(rand.NewSource(20261001))
	e := risk.NewEngine()

	// 初始载入，不计入耗时。
	for i := 0; i < numStudents; i++ {
		e.AddStudent(studentID(i), randFacts(rng))
	}
	runtime.GC()
	baseRSS := peakRSSMiB()

	// 采样点：最早、中间、最新三份报告，发布后抓取独立快照（不计时）。
	sampleIDs := map[int]bool{1: true, numPublishes / 2: true, numPublishes: true}
	snaps := map[int]map[string]risk.Facts{}
	snapWeights := map[int][2]risk.Weights{}

	start := time.Now()
	curWeights := risk.Weights{GPA: 25, Attendance: 20, MidtermDFW: 15, NoAid: 10, FirstGen: 10}
	for p := 1; p <= numPublishes; p++ {
		for k := 0; k < replacePerPub; k++ {
			e.ReplaceFacts(studentID(rng.Intn(numStudents)), randFacts(rng))
		}
		w := randWeights(rng)
		if p%7 == 0 {
			w = curWeights // 权重不变也产生报告
		}
		repID := e.Publish(w)
		if repID != p {
			fmt.Printf("FAIL: 报告ID不连续 got %d want %d\n", repID, p)
			os.Exit(1)
		}
		if sampleIDs[p] {
			snapWeights[p] = [2]risk.Weights{curWeights, w}
		}
		curWeights = w
	}
	// 10 万次历史单学生查询：均匀覆盖最早/中间/最新报告与新老学生。
	var checksum int
	for q := 0; q < numQueries; q++ {
		var repID int
		switch q % 3 {
		case 0:
			repID = 1
		case 1:
			repID = numPublishes / 2
		default:
			repID = numPublishes
		}
		rec := e.StudentInReport(repID, studentID(rng.Intn(numStudents)))
		if rec.InPopulation {
			checksum += rec.OldScore + rec.NewScore
		}
	}
	elapsed := time.Since(start)
	runtime.GC()
	peakRSS := peakRSSMiB()

	// 独立对照：抓取采样报告发布时的快照需要逐人当时事实。
	// 库不暴露内部状态，这里用库自身的学生解释接口逐人导出采样报告的事实，
	// 再用独立计分实现重算分布与迁移矩阵，与报告计数对照。
	ok := true
	for repID := range sampleIDs {
		pop := make(map[string]risk.Facts, numStudents)
		rep := e.Report(repID)
		for i := 0; i < numStudents; i++ {
			rec := e.StudentInReport(repID, studentID(i))
			if rec.InPopulation {
				pop[studentID(i)] = rec.Facts
			}
		}
		snaps[repID] = pop
		var oldCounts, newCounts [3]int
		var trans [3][3]int
		ws := snapWeights[repID]
		for _, f := range pop {
			ot := refTier(refScore(ws[0], f))
			nt := refTier(refScore(ws[1], f))
			oldCounts[ot]++
			newCounts[nt]++
			trans[ot][nt]++
		}
		match := rep.TotalStudents == len(pop) &&
			rep.OldCounts == oldCounts && rep.NewCounts == newCounts && rep.Transitions == trans
		if !match {
			ok = false
			fmt.Printf("FAIL: 报告%d 对照不一致\n got  total=%d old=%v new=%v trans=%v\n want total=%d old=%v new=%v trans=%v\n",
				repID, rep.TotalStudents, rep.OldCounts, rep.NewCounts, rep.Transitions,
				len(pop), oldCounts, newCounts, trans)
		} else {
			fmt.Printf("报告%d 对照一致: total=%d old=%v new=%v\n",
				repID, rep.TotalStudents, rep.OldCounts, rep.NewCounts)
		}
	}

	fmt.Printf("操作耗时: %.3f 秒 (预算 %d 秒)\n", elapsed.Seconds(), timeBudgetSecs)
	fmt.Printf("峰值内存: %.1f MiB (载入后基线 %.1f MiB, 预算 %d MiB)\n", peakRSS, baseRSS, memBudgetMiB)
	fmt.Printf("查询校验和: %d\n", checksum)
	if elapsed.Seconds() > timeBudgetSecs {
		ok = false
		fmt.Println("FAIL: 超出时间预算")
	}
	if peakRSS > memBudgetMiB {
		ok = false
		fmt.Println("FAIL: 超出内存预算")
	}
	if !ok {
		os.Exit(1)
	}
	fmt.Println("PASS: 规模验证通过")
}
