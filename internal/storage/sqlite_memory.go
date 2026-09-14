package storage

import "time"

// Memory 定義專案級基礎記憶結構 (MEM-001)
type Memory struct {
	ID        string    `json:"id"`
	Workspace string    `json:"workspace"`
	Content   string    `json:"content"`
	Source    string    `json:"source"`
	CreatedAt time.Time `json:"created_at"`
}
