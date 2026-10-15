package monitor

import (
	"time"

	"v2link-go/internal/store"
)

// BucketSize 是聚合桶粒度。
type BucketSize string

const (
	// BucketHour 按本地时区整点聚合。
	BucketHour BucketSize = "hour"
	// BucketDay 按本地时区当天 0 点聚合。
	BucketDay BucketSize = "day"
)

// HourMS / DayMS 是桶步长。
const (
	HourMS = 3600 * 1000
	DayMS  = 24 * HourMS
)

// TrafficPoint 是对外展示的一个桶；TS 为桶起始（epoch 毫秒，本地时区对齐）。
type TrafficPoint struct {
	TS   int64 `json:"ts"`
	Up   int64 `json:"up"`
	Down int64 `json:"down"`
}

// ParseBucket 解析 bucket 查询参数：非法/缺省一律 hour。
func ParseBucket(raw string) BucketSize {
	if raw == "day" {
		return BucketDay
	}
	return BucketHour
}

// BucketStart 返回 ts 所在桶的起始（本地时区）。
//
// 不能直接对 epoch 取整（会偏到 UTC 整点），必须先按本地日历算出当天 0 点再对齐。
func BucketStart(ts int64, bucket BucketSize) int64 {
	t := time.UnixMilli(ts)
	dayStart := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location()).UnixMilli()
	if bucket == BucketDay {
		return dayStart
	}
	hourIdx := floorDiv(ts-dayStart, HourMS)
	return dayStart + hourIdx*HourMS
}

// BucketSeries 生成 [from, to) 内连续桶骨架（含无数据的桶，前端要连续横轴）。
func BucketSeries(from, to int64, bucket BucketSize) []int64 {
	step := int64(HourMS)
	if bucket == BucketDay {
		step = DayMS
	}
	out := make([]int64, 0)
	for t := BucketStart(from, bucket); t < to; t += step {
		out = append(out, t)
	}
	return out
}

// Aggregate 把采样明细按桶聚合（仅含有数据的桶）。
func Aggregate(samples []store.TrafficSampleRow, bucket BucketSize) map[int64]TrafficPoint {
	acc := make(map[int64]TrafficPoint)
	for _, s := range samples {
		b := BucketStart(s.TS, bucket)
		cur := acc[b]
		cur.TS = b
		cur.Up += s.UpDelta
		cur.Down += s.DownDelta
		acc[b] = cur
	}
	return acc
}

// ToSeries 把桶骨架与数据合并为连续点序列（空桶补 0）。
func ToSeries(acc map[int64]TrafficPoint, from, to int64, bucket BucketSize) []TrafficPoint {
	series := BucketSeries(from, to, bucket)
	out := make([]TrafficPoint, 0, len(series))
	for _, ts := range series {
		if p, ok := acc[ts]; ok {
			out = append(out, p)
			continue
		}
		out = append(out, TrafficPoint{TS: ts})
	}
	return out
}

// floorDiv 对正负皆向下取整（Go 的 / 是截断除法）。
func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}
