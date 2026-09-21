package backend

import (
	"fmt"
	"sort"
	"sync"
)

// Registry 定義後端註冊表介面
type Registry interface {
	// Register 註冊後端實例，若已存在相同 ID 則返回錯誤
	Register(b Backend) error
	// Get 依據 ID 取得後端實例，不存在時返回 ErrBackendNotFound
	Get(id string) (Backend, error)
	// List 列出所有已註冊之後端 ID (排序)
	List() []string
	// Default 取得預設後端
	Default() Backend
	// SetDefault 設定預設後端 ID
	SetDefault(id string) error
	// Unregister 註銷指定後端 ID
	Unregister(id string)
}

// InMemoryRegistry 實作線程安全的記憶體後端註冊表
type InMemoryRegistry struct {
	mu          sync.RWMutex
	backends    map[string]Backend
	defaultName string
}

// NewRegistry 建立新的 InMemoryRegistry
func NewRegistry() *InMemoryRegistry {
	return &InMemoryRegistry{
		backends: make(map[string]Backend),
	}
}

// Register 註冊後端
func (r *InMemoryRegistry) Register(b Backend) error {
	if b == nil {
		return fmt.Errorf("cannot register nil backend")
	}
	id := b.ID()
	if id == "" {
		return fmt.Errorf("backend ID cannot be empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.backends[id]; exists {
		return fmt.Errorf("backend with ID %q already registered", id)
	}

	r.backends[id] = b
	if r.defaultName == "" {
		r.defaultName = id
	}
	return nil
}

// Unregister 註銷後端
func (r *InMemoryRegistry) Unregister(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.backends, id)
	if r.defaultName == id {
		r.defaultName = ""
	}
}

// Get 取得指定後端，查無後端時返回 ErrBackendNotFound
func (r *InMemoryRegistry) Get(id string) (Backend, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if b, exists := r.backends[id]; exists {
		return b, nil
	}
	return nil, fmt.Errorf("%w: %q", ErrBackendNotFound, id)
}

// List 列出所有後端 ID
func (r *InMemoryRegistry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	list := make([]string, 0, len(r.backends))
	for id := range r.backends {
		list = append(list, id)
	}
	sort.Strings(list)
	return list
}

// Default 取得預設後端
func (r *InMemoryRegistry) Default() Backend {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.defaultName == "" {
		return nil
	}
	return r.backends[r.defaultName]
}

// SetDefault 設定預設後端
func (r *InMemoryRegistry) SetDefault(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.backends[id]; !exists {
		return fmt.Errorf("%w: %q", ErrBackendNotFound, id)
	}
	r.defaultName = id
	return nil
}
