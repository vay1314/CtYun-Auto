package update

import (
	"errors"
	"sync"
	"time"
)

// Manager serializes update tasks so only one runs at a time.
type Manager struct {
	mu       sync.Mutex
	active   bool
	progress Progress
	dataDir  string
	lock     *fileLock
}

func NewManager(dataDir ...string) *Manager {
	manager := &Manager{progress: Progress{Status: StatusIdle}}
	if len(dataDir) > 0 {
		manager.dataDir = dataDir[0]
	}
	return manager
}

func (m *Manager) Begin() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active {
		return errors.New("已有更新任务正在进行")
	}
	lock, err := acquireFileLock(m.dataDir)
	if err != nil {
		return err
	}
	m.active = true
	m.lock = lock
	m.progress = Progress{Status: StatusChecking}
	return nil
}

func (m *Manager) SetProgress(p Progress) {
	m.mu.Lock()
	if p.Percent < 0 {
		p.Percent = 0
	}
	if p.Percent > 100 {
		p.Percent = 100
	}
	p.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	m.progress = p
	m.mu.Unlock()
}

func (m *Manager) Progress() Progress {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.progress
}

func (m *Manager) Finish() {
	m.mu.Lock()
	m.active = false
	lock := m.lock
	m.lock = nil
	m.mu.Unlock()
	lock.release()
}

func (m *Manager) HandOff(token ...string) error {
	m.mu.Lock()
	if len(token) > 0 && m.lock != nil {
		if err := m.lock.handOff(token[0]); err != nil {
			m.mu.Unlock()
			m.Fail(err)
			return err
		}
	}
	m.active = false
	m.lock = nil
	m.mu.Unlock()
	return nil
}

func (m *Manager) Fail(err error) {
	message := "更新失败"
	if err != nil {
		message = err.Error()
	}
	m.SetProgress(Progress{Status: StatusFailed, Message: message})
	m.Finish()
}

func (m *Manager) Active() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.active
}
