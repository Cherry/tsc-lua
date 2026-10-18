package main

import (
	"fmt"
	"slices"
	"sync/atomic"
)

type Arena[T any] struct{ data []T }

func (a *Arena[T]) New() *T {
	if len(a.data) == cap(a.data) {
		a.data = slices.Grow[[]T](nil, max(len(a.data)*2, 1))
	}
	index := len(a.data)
	a.data = a.data[:index+1]
	return &a.data[index]
}

type symbolData struct {
	name  string
	decls []int
}

type Symbol struct {
	flags uint32
	id    atomic.Uint64
	data  *symbolData
}

func (s *Symbol) Name() string      { return s.data.name }
func (s *Symbol) SetName(n string)  { s.data.name = n }
func (s *Symbol) SetData(o *Symbol) { s.data = o.data }
func (s *Symbol) SetFlags(f uint32) { s.flags = f }
func (s *Symbol) Flags() uint32     { return s.flags }

type SymbolWithData struct {
	s Symbol
	d symbolData
}

func (sd *SymbolWithData) Initialize() *Symbol {
	sd.s.data = &sd.d
	return &sd.s
}

type Links struct {
	target *Symbol
	mapper int
}

type LinkStore[K comparable, V any] struct {
	entries map[K]*V
	arena   Arena[V]
}

func (s *LinkStore[K, V]) Get(key K) *V {
	value := s.entries[key]
	if value != nil {
		return value
	}
	if s.entries == nil {
		s.entries = make(map[K]*V)
	}
	value = s.arena.New()
	s.entries[key] = value
	return value
}

type Checker struct {
	symbolArena         Arena[Symbol]
	symbolWithDataArena Arena[SymbolWithData]
	links               LinkStore[*Symbol, Links]
}

func (c *Checker) newSymbol(flags uint32, name string) *Symbol {
	result := c.symbolWithDataArena.New().Initialize()
	result.SetFlags(flags)
	result.SetName(name)
	return result
}

func (c *Checker) newShared(s *Symbol) *Symbol {
	result := c.symbolArena.New()
	result.SetFlags(s.Flags() | 0x1000)
	result.SetData(s)
	return result
}

func main() {
	c := &Checker{}
	var syms []*Symbol
	for i := 0; i < 20; i++ {
		syms = append(syms, c.newSymbol(uint32(i), fmt.Sprintf("s%d", i)))
	}
	var shared []*Symbol
	for i, s := range syms {
		sh := c.newShared(s)
		l := c.links.Get(sh)
		l.target = s
		l.mapper = i
		shared = append(shared, sh)
	}
	for i, sh := range shared {
		l := c.links.Get(sh)
		fmt.Println(i, sh.Name(), sh.Flags(), l.target.Name(), l.mapper, sh != syms[i], sh.data == syms[i].data)
	}
}
