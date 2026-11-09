// Package raft реализует выборы и репликацию лога Raft.
package raft

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"slices"
	"sync"
	"time"
)

type Role int

const (
	Follower Role = iota
	Candidate
	Leader
)

func (r Role) String() string {
	switch r {
	case Follower:
		return "Follower"
	case Candidate:
		return "Candidate"
	case Leader:
		return "Leader"
	default:
		return "Unknown"
	}
}

const (
	defaultHeartbeatInterval  = 50 * time.Millisecond
	defaultRequestVoteTimeout = 200 * time.Millisecond
	defaultElectionMin        = 150 * time.Millisecond
	defaultElectionMax        = 300 * time.Millisecond
	defaultMaxEntriesPerRPC   = 64
)

// Config задаёт участников, зависимости и таймауты узла.
type Config struct {
	ID    NodeID
	Peers []NodeID // Остальные участники без собственного ID.

	Transport Transport
	Storage   Storage
	Clock     Clock // По умолчанию SystemClock.

	// Подтверждённые записи идут в ApplyCh по порядку; узел не закрывает канал.
	ApplyCh chan<- ApplyMsg

	// При каждом сбросе выбираем случайный таймаут из заданного диапазона.
	ElectionTimeoutMin time.Duration
	ElectionTimeoutMax time.Duration
	Rand               *rand.Rand
	// Heartbeat должен приходить заметно чаще таймаута выборов.
	HeartbeatInterval  time.Duration
	RequestVoteTimeout time.Duration
	// Ограничиваем ожидание RPC, чтобы медленный сосед не задерживал следующий heartbeat.
	AppendEntriesTimeout time.Duration
	// Ограничиваем число записей в одной порции репликации.
	MaxEntriesPerRPC int

	Logger *slog.Logger

	// OnFatal должен завершить узел при нарушении протокола и не возвращаться.
	OnFatal func(msg string)

	electionTimeout func() time.Duration
}

func (c *Config) validateAndSetDefaults() error {
	if c.Logger == nil {
		c.Logger = slog.New(slog.DiscardHandler)
	}

	var errs []error
	if c.ID == "" {
		errs = append(errs, errors.New("ID is required"))
	}
	if slices.Contains(c.Peers, c.ID) {
		errs = append(errs, fmt.Errorf("peers must not contain own ID %q", c.ID))
	}
	if slices.Contains(c.Peers, "") {
		errs = append(errs, errors.New("peers must not contain an empty ID"))
	}
	// Повтор ID не должен увеличивать число голосов и размер кворума.
	if dups := duplicates(c.Peers); len(dups) > 0 {
		errs = append(errs, fmt.Errorf("duplicate peers %v", dups))
	}
	if c.Transport == nil {
		errs = append(errs, errors.New("transport is required"))
	}
	if c.Storage == nil {
		errs = append(errs, errors.New("storage is required"))
	}
	if c.ApplyCh == nil {
		errs = append(errs, errors.New("apply channel is required"))
	}
	if c.MaxEntriesPerRPC < 0 {
		errs = append(errs, fmt.Errorf("negative max entries per RPC %d", c.MaxEntriesPerRPC))
	}
	if c.HeartbeatInterval < 0 {
		errs = append(errs, fmt.Errorf("negative heartbeat interval %v", c.HeartbeatInterval))
	}
	if c.RequestVoteTimeout < 0 {
		errs = append(errs, fmt.Errorf("negative RequestVote timeout %v", c.RequestVoteTimeout))
	}
	if c.AppendEntriesTimeout < 0 {
		errs = append(errs, fmt.Errorf("negative AppendEntries timeout %v", c.AppendEntriesTimeout))
	}
	if c.ElectionTimeoutMin < 0 || c.ElectionTimeoutMax < 0 {
		errs = append(errs, fmt.Errorf("negative election timeout bounds [%v, %v)",
			c.ElectionTimeoutMin, c.ElectionTimeoutMax))
	}
	electionMin := cmp.Or(c.ElectionTimeoutMin, defaultElectionMin)
	electionMax := cmp.Or(c.ElectionTimeoutMax, defaultElectionMax)
	// Диапазон должен оставлять случайный разброс таймаутов выборов.
	if electionMax <= electionMin {
		errs = append(errs, fmt.Errorf("election timeout max %v must be above min %v", electionMax, electionMin))
	}
	hb := cmp.Or(c.HeartbeatInterval, defaultHeartbeatInterval)
	if hb >= electionMin {
		errs = append(errs, fmt.Errorf("heartbeat interval %v must be below the minimum election timeout %v",
			hb, electionMin))
	}
	if aet := cmp.Or(c.AppendEntriesTimeout, hb); aet >= electionMin {
		errs = append(errs, fmt.Errorf("AppendEntries timeout %v must be below the minimum election timeout %v",
			aet, electionMin))
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("raft: invalid config: %w", err)
	}

	if c.Clock == nil {
		c.Clock = SystemClock{}
	}
	c.ElectionTimeoutMin, c.ElectionTimeoutMax = electionMin, electionMax
	if c.electionTimeout == nil {
		span := electionMax - electionMin
		if r := c.Rand; r != nil {
			c.electionTimeout = func() time.Duration { return electionMin + time.Duration(r.Int64N(int64(span))) }
		} else {
			c.electionTimeout = func() time.Duration { return electionMin + rand.N(span) }
		}
	}
	c.HeartbeatInterval = hb
	c.AppendEntriesTimeout = cmp.Or(c.AppendEntriesTimeout, hb)
	if c.RequestVoteTimeout == 0 {
		c.RequestVoteTimeout = defaultRequestVoteTimeout
	}
	if c.MaxEntriesPerRPC == 0 {
		c.MaxEntriesPerRPC = defaultMaxEntriesPerRPC
	}
	if c.OnFatal == nil {
		c.OnFatal = func(msg string) {
			fmt.Fprintln(os.Stderr, msg)
			os.Exit(1)
		}
	}
	return nil
}

func duplicates(ids []NodeID) []NodeID {
	seen := make(map[NodeID]int, len(ids))
	var dups []NodeID
	for _, id := range ids {
		seen[id]++
		if seen[id] == 2 {
			dups = append(dups, id)
		}
	}
	return dups
}

type peer struct {
	id   NodeID
	wake chan struct{}
}

type Raft struct {
	id                   NodeID
	peers                []peer
	transport            Transport
	storage              Storage
	clock                Clock
	electionTimeout      func() time.Duration
	heartbeatInterval    time.Duration
	requestVoteTimeout   time.Duration
	appendEntriesTimeout time.Duration
	maxEntriesPerRPC     int
	applyCh              chan<- ApplyMsg
	applyWake            chan struct{}
	logger               *slog.Logger
	onFatal              func(msg string)
	electionTimer        Timer
	heartbeatTimer       Timer
	// Тестовый обработчик смены лидера вызывается под mutex.
	onBecomeLeader func(term Term)

	// При остановке ждём фоновые горутины; добавляем их только из Run и его обработчиков таймеров.
	wg sync.WaitGroup

	lastAppendFailureLog map[NodeID]time.Time

	mu          sync.Mutex
	started     bool
	done        <-chan struct{}
	role        Role
	currentTerm Term
	votedFor    NodeID
	// Известный лидер текущего term; пустая строка означает, что лидер неизвестен.
	leaderID NodeID
	// Считаем уникальные голоса текущей кандидатуры, включая собственный.
	votes map[NodeID]struct{}
	// Контекст отменяет исходящие RPC при смене term или роли.
	rpcCtx    context.Context
	rpcCancel context.CancelFunc
	// Текущий term или голос ещё не сохранён в Storage.
	dirty            bool
	electionDeadline time.Time

	// Рабочая копия лога; изменения сначала сохраняются в Storage.
	log []LogEntry
	// Последний известный подтверждённый индекс; после перезапуска уточняется заново.
	commitIndex Index
	// nextIndex — следующая позиция для отправки; matchIndex — подтверждённый соседом префикс.
	nextIndex  map[NodeID]Index
	matchIndex map[NodeID]Index
	// Соседи, ответившие лидеру после последней проверки кворума.
	heardFrom      map[NodeID]struct{}
	mismatchLogged map[NodeID]struct{}
}

// New загружает состояние и лог из Storage и создаёт узел в роли Follower.
func New(cfg Config) (*Raft, error) {
	if err := cfg.validateAndSetDefaults(); err != nil {
		return nil, err
	}

	state, err := cfg.Storage.LoadState()
	if err != nil {
		return nil, fmt.Errorf("raft: load state: %w", err)
	}
	entries, err := cfg.Storage.LoadLog()
	if err != nil {
		return nil, fmt.Errorf("raft: load log: %w", err)
	}
	// Сохранённый лог не должен содержать записи новее сохранённого term.
	if err := checkEntries(0, entries, state.CurrentTerm); err != nil {
		return nil, fmt.Errorf("raft: invalid stored log: %w", err)
	}

	rf := &Raft{
		id:                   cfg.ID,
		transport:            cfg.Transport,
		storage:              cfg.Storage,
		clock:                cfg.Clock,
		electionTimeout:      cfg.electionTimeout,
		heartbeatInterval:    cfg.HeartbeatInterval,
		requestVoteTimeout:   cfg.RequestVoteTimeout,
		appendEntriesTimeout: cfg.AppendEntriesTimeout,
		maxEntriesPerRPC:     cfg.MaxEntriesPerRPC,
		applyCh:              cfg.ApplyCh,
		applyWake:            make(chan struct{}, 1),
		logger:               cfg.Logger.With("node", cfg.ID),
		onFatal:              cfg.OnFatal,
		electionTimer:        cfg.Clock.NewTimer(0),
		heartbeatTimer:       cfg.Clock.NewTimer(0),
		role:                 Follower,
		currentTerm:          state.CurrentTerm,
		votedFor:             state.VotedFor,
		log:                  append([]LogEntry{{}}, entries...),
	}
	rf.electionTimer.Stop()
	rf.heartbeatTimer.Stop()
	rf.peers = make([]peer, len(cfg.Peers))
	for i, id := range cfg.Peers {
		rf.peers[i] = peer{id: id, wake: make(chan struct{}, 1)}
	}
	return rf, nil
}

// Возвращаем текущий term и признак действующего лидера.
func (rf *Raft) GetState() (term Term, isLeader bool) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	return rf.currentTerm, rf.isLeaderLocked()
}

// Возвращаем известного лидера или пустую строку.
func (rf *Raft) Leader() NodeID {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	return rf.leaderLocked()
}

func (rf *Raft) leaderLocked() NodeID {
	if rf.stoppedLocked() {
		return ""
	}
	return rf.leaderID
}

func (rf *Raft) isLeaderLocked() bool {
	return rf.role == Leader && !rf.stoppedLocked()
}

func (rf *Raft) stoppedLocked() bool {
	select {
	case <-rf.done:
		return true
	default:
		return false
	}
}

// Завершаем узел при нарушении внутреннего ограничения протокола.
func (rf *Raft) fatalf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	rf.logger.Error("invariant violated", "msg", msg)
	rf.onFatal("raft: " + msg)
	panic("raft: OnFatal returned after: " + msg)
}

// Большинство фиксированного состава с учётом самого узла.
func (rf *Raft) quorum() int {
	return (len(rf.peers)+1)/2 + 1
}

// После победы сохраняем no-op и начинаем работу лидера.
func (rf *Raft) becomeLeaderLocked() {
	// Без сохранённого no-op узел остаётся кандидатом.
	noop := LogEntry{Term: rf.currentTerm, Kind: EntryNoOp}
	if err := rf.storeLogLocked(rf.lastLogIndexLocked()+1, []LogEntry{noop}); err != nil {
		return
	}
	rf.logger.Info("became leader", "term", rf.currentTerm, "votes", len(rf.votes),
		"lastLogIndex", rf.lastLogIndexLocked())
	rf.role = Leader
	rf.leaderID = rf.id
	rf.initLeaderStateLocked()
	if rf.onBecomeLeader != nil {
		rf.onBecomeLeader(rf.currentTerm)
	}
	rf.cancelRPCsLocked()
	// Таймер выборов лидера теперь запускает проверку кворума.
	rf.resetElectionTimerLocked()
	rf.advanceCommitLocked()
	// Первый heartbeat отправляем сразу после победы.
	rf.heartbeatTimer.Reset(0)
}

// Новый лидер заново определяет, какие позиции есть у каждого соседа.
func (rf *Raft) initLeaderStateLocked() {
	rf.nextIndex = make(map[NodeID]Index, len(rf.peers))
	rf.matchIndex = make(map[NodeID]Index, len(rf.peers))
	for _, p := range rf.peers {
		rf.nextIndex[p.id] = rf.lastLogIndexLocked() + 1
		rf.matchIndex[p.id] = 0
	}
	rf.heardFrom = make(map[NodeID]struct{}, len(rf.peers))
	rf.mismatchLogged = make(map[NodeID]struct{})
}

// Принимаем больший term и отступаем; перед ответами новый term сохранит syncLocked.
func (rf *Raft) adoptHigherTermLocked(term Term) {
	if term <= rf.currentTerm {
		// Сбрасывать голос в прежнем term нельзя: это разрешило бы проголосовать повторно.
		rf.fatalf("adopting term %d, but current term is already %d", term, rf.currentTerm)
	}
	rf.logger.Info("adopted higher term, stepping down",
		"oldTerm", rf.currentTerm, "term", term, "oldRole", rf.role)
	rf.currentTerm = term
	rf.votedFor = ""
	rf.leaderID = ""
	rf.dirty = true
	rf.cancelRPCsLocked()

	if rf.role == Leader {
		rf.stepDownLocked()
	} else {
		rf.role = Follower
	}
}

// Лидер отступает, если за окно наблюдения не получил ответы большинства.
func (rf *Raft) checkQuorumLocked() {
	if len(rf.heardFrom)+1 >= rf.quorum() {
		clear(rf.heardFrom)
		rf.resetElectionTimerLocked()
		return
	}
	rf.logger.Info("lost contact with a quorum, stepping down",
		"term", rf.currentTerm, "heardFrom", len(rf.heardFrom))
	rf.stepDownLocked()
}

func (rf *Raft) stepDownLocked() {
	rf.role = Follower
	rf.leaderID = ""
	rf.heartbeatTimer.Stop()
	rf.resetElectionTimerLocked()
	rf.cancelRPCsLocked()
}

func (rf *Raft) resetElectionTimerLocked() {
	d := rf.electionTimeout()
	rf.electionDeadline = rf.clock.Now().Add(d)
	rf.electionTimer.Reset(d)
}

// Создаём общий контекст исходящих RPC текущего term и роли.
func (rf *Raft) rpcContextLocked(parent context.Context) context.Context {
	if rf.rpcCtx == nil {
		rf.rpcCtx, rf.rpcCancel = context.WithCancel(parent)
	}
	return rf.rpcCtx
}

// Ограничиваем RPC по часам узла; причину таймаута можно получить через context.Cause.
func (rf *Raft) withTimeout(parent context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancelCause(parent)
	timer := rf.clock.NewTimer(d)
	go func() {
		defer timer.Stop()
		select {
		case <-timer.C():
			cancel(context.DeadlineExceeded)
		case <-ctx.Done():
		}
	}()
	return ctx, func() { cancel(context.Canceled) }
}

// Отменяем незавершённые RPC при смене term или роли.
func (rf *Raft) cancelRPCsLocked() {
	if rf.rpcCancel != nil {
		rf.rpcCancel()
		rf.rpcCtx, rf.rpcCancel = nil, nil
	}
}
