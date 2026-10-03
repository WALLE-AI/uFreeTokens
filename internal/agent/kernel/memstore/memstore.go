// Package memstore 是 kernel.Store 的内存实现，供单测使用。
package memstore

import (
	"context"
	"sync"
	"time"

	"github.com/WALLE-AI/uFreeTokens/internal/agent/kernel"
)

// Store 记录全部写入，测试可直接检查字段。
type Store struct {
	mu        sync.Mutex
	Messages  map[int64][]kernel.Message
	Calls     map[string]*kernel.ToolCallRecord
	Proposals []*kernel.ProposalRecord
	Usage     map[int64]kernel.Usage
	Turns     map[int64]int
	Cancelled map[int64]bool
}

func New() *Store {
	return &Store{
		Messages: map[int64][]kernel.Message{}, Calls: map[string]*kernel.ToolCallRecord{},
		Usage: map[int64]kernel.Usage{}, Turns: map[int64]int{}, Cancelled: map[int64]bool{},
	}
}

func (s *Store) AppendMessage(_ context.Context, sid int64, m *kernel.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m.Seq = len(s.Messages[sid]) + 1
	m.CreatedAt = time.Now()
	s.Messages[sid] = append(s.Messages[sid], *m)
	return nil
}

func (s *Store) SaveToolCall(_ context.Context, rec *kernel.ToolCallRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *rec
	s.Calls[rec.ID] = &cp
	return nil
}

func (s *Store) FinishToolCall(ctx context.Context, rec *kernel.ToolCallRecord) error {
	return s.SaveToolCall(ctx, rec)
}

func (s *Store) SaveProposal(_ context.Context, rec *kernel.ToolCallRecord, p *kernel.ProposalRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, q := range s.Proposals {
		if q.TargetType == p.TargetType && q.TargetID == p.TargetID && q.Tool == p.Tool && s.Calls[q.ToolCallID].Status == kernel.CallPendingApproval {
			return kernel.ErrDuplicateProposal
		}
	}
	cp := *rec
	s.Calls[rec.ID] = &cp
	p.ID = int64(len(s.Proposals) + 1)
	pc := *p
	s.Proposals = append(s.Proposals, &pc)
	return nil
}

func (s *Store) AddUsage(_ context.Context, sid int64, u kernel.Usage, turns int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.Usage[sid]
	cur.In += u.In
	cur.Out += u.Out
	s.Usage[sid] = cur
	s.Turns[sid] += turns
	return nil
}

func (s *Store) CancelRequested(_ context.Context, sid int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Cancelled[sid], nil
}

// History 返回会话消息的副本。
func (s *Store) History(sid int64) []kernel.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]kernel.Message(nil), s.Messages[sid]...)
}
