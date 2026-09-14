package cron

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

var (
	ErrInvalidExpression = errors.New("invalid cron expression")
)

// Schedule 代表已解析完成之標準 5 欄位 Cron 排程
type Schedule struct {
	expr     string
	minutes  map[int]bool // 0-59
	hours    map[int]bool // 0-23
	days     map[int]bool // 1-31
	months   map[int]bool // 1-12
	weekdays map[int]bool // 0-6 (0=Sunday, 6=Saturday)
}

// Parse 解析標準 5 欄位 Cron 表達式（分 時 日 月 週）
// 遵循 CRON-003：純標準庫實作，零重型外部相依
func Parse(expr string) (*Schedule, error) {
	fields := strings.Fields(strings.TrimSpace(expr))
	if len(fields) != 5 {
		return nil, fmt.Errorf("%w: expected 5 fields, got %d", ErrInvalidExpression, len(fields))
	}

	minutes, err := parseField(fields[0], 0, 59, false)
	if err != nil {
		return nil, fmt.Errorf("%w: minute field error: %v", ErrInvalidExpression, err)
	}

	hours, err := parseField(fields[1], 0, 23, false)
	if err != nil {
		return nil, fmt.Errorf("%w: hour field error: %v", ErrInvalidExpression, err)
	}

	days, err := parseField(fields[2], 1, 31, false)
	if err != nil {
		return nil, fmt.Errorf("%w: day-of-month field error: %v", ErrInvalidExpression, err)
	}

	months, err := parseField(fields[3], 1, 12, false)
	if err != nil {
		return nil, fmt.Errorf("%w: month field error: %v", ErrInvalidExpression, err)
	}

	weekdays, err := parseField(fields[4], 0, 7, true)
	if err != nil {
		return nil, fmt.Errorf("%w: weekday field error: %v", ErrInvalidExpression, err)
	}

	return &Schedule{
		expr:     expr,
		minutes:  minutes,
		hours:    hours,
		days:     days,
		months:   months,
		weekdays: weekdays,
	}, nil
}

func parseField(field string, min, max int, isWeekday bool) (map[int]bool, error) {
	result := make(map[int]bool)
	parts := strings.Split(field, ",")
	if len(parts) == 0 {
		return nil, errors.New("empty field")
	}

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, errors.New("empty part in list")
		}

		step := 1
		rangeStr := part
		if strings.Contains(part, "/") {
			subParts := strings.Split(part, "/")
			if len(subParts) != 2 {
				return nil, fmt.Errorf("invalid step syntax: %s", part)
			}
			rangeStr = subParts[0]
			var err error
			step, err = strconv.Atoi(subParts[1])
			if err != nil || step <= 0 {
				return nil, fmt.Errorf("invalid step value: %s", subParts[1])
			}
		}

		var start, end int
		if rangeStr == "*" {
			start = min
			end = max
		} else if strings.Contains(rangeStr, "-") {
			rangeParts := strings.Split(rangeStr, "-")
			if len(rangeParts) != 2 {
				return nil, fmt.Errorf("invalid range syntax: %s", rangeStr)
			}
			var err error
			start, err = strconv.Atoi(rangeParts[0])
			if err != nil {
				return nil, fmt.Errorf("invalid range start: %s", rangeParts[0])
			}
			end, err = strconv.Atoi(rangeParts[1])
			if err != nil {
				return nil, fmt.Errorf("invalid range end: %s", rangeParts[1])
			}
			if start > end {
				return nil, fmt.Errorf("range start %d greater than end %d", start, end)
			}
		} else {
			val, err := strconv.Atoi(rangeStr)
			if err != nil {
				return nil, fmt.Errorf("invalid number: %s", rangeStr)
			}
			start = val
			end = val
		}

		if start < min || end > max {
			return nil, fmt.Errorf("value out of range [%d, %d]: %s", min, max, part)
		}

		for i := start; i <= end; i += step {
			val := i
			if isWeekday && val == 7 {
				val = 0 // 7 亦代表週日
			}
			result[val] = true
		}
	}

	return result, nil
}

// Next 計算相對於給定基準時間之「下次觸發時間」
// 遵循 CRON-003：採用本地時區計算，秒數對齊 00
func (s *Schedule) Next(from time.Time) time.Time {
	// 截斷至整分並前進 1 分鐘，尋找嚴格大於 from 的最近整分
	t := from.Truncate(time.Minute).Add(time.Minute)

	// 最多搜尋 50 年防止死循環（例如 2 月 31 日，或最長約 40 年一遇之閏年特定星期）
	maxTime := t.AddDate(50, 0, 0)

	for t.Before(maxTime) {
		// 1. 檢查月份
		if !s.months[int(t.Month())] {
			// 快進到下個月 1 號 00:00
			newT := time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, t.Location())
			if !newT.After(t) {
				newT = t.AddDate(0, 1, 0)
			}
			t = newT
			continue
		}

		// 2. 檢查日期與星期
		dayMatch := s.days[t.Day()]
		weekdayMatch := s.weekdays[int(t.Weekday())]

		if !dayMatch || !weekdayMatch {
			// 快進到隔天 00:00
			newT := time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, t.Location())
			if !newT.After(t) {
				newT = t.AddDate(0, 0, 1)
			}
			t = newT
			continue
		}

		// 3. 檢查小時
		if !s.hours[t.Hour()] {
			// 快進到下一小時 00 分，防範 DST 跳躍 gap 造成 time.Date 正規化回較早時刻引發死循環
			newT := time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, t.Location())
			if !newT.After(t) {
				newT = t.Add(time.Hour)
				newT = time.Date(newT.Year(), newT.Month(), newT.Day(), newT.Hour(), 0, 0, 0, newT.Location())
				if !newT.After(t) {
					newT = t.Add(time.Minute)
				}
			}
			t = newT
			continue
		}

		// 4. 檢查分鐘
		if !s.minutes[t.Minute()] {
			t = t.Add(time.Minute)
			continue
		}

		return t
	}

	return time.Time{}
}

// IsReachable 檢查在未來的時間窗口內是否至少存在一次觸發時間（防範如 2 月 31 日等日曆無效日期）
func (s *Schedule) IsReachable(from time.Time) bool {
	next := s.Next(from)
	return !next.IsZero()
}

