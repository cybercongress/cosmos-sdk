package simsx

import (
	sdk "github.com/cosmos/cosmos-sdk/types"
	"golang.org/x/exp/maps"
	"iter"
	"math/rand"
	"slices"
	"strings"
)

var _ Registry = &SimsV2Reg{}

type SimsV2Reg map[string]weightedFactory

func (s SimsV2Reg) Add(weight uint32, f SimMsgFactoryX) {
	msgType := f.MsgType()
	msgTypeURL := sdk.MsgTypeURL(msgType)
	if _, exists := s[msgTypeURL]; exists {
		panic("type is already registered: " + msgTypeURL)
	}
	s[msgTypeURL] = weightedFactory{weight: weight, factory: f}
}

func (s SimsV2Reg) NextFactoryFn(r *rand.Rand) func() SimMsgFactoryX {
	factories := maps.Values(s)
	slices.SortFunc(factories, func(a, b weightedFactory) int { // sort to make deterministic
		return strings.Compare(sdk.MsgTypeURL(a.factory.MsgType()), sdk.MsgTypeURL(b.factory.MsgType()))
	})
	r.Shuffle(len(factories), func(i, j int) {
		factories[i], factories[j] = factories[j], factories[i]
	})
	var totalWeight int
	for k := range factories {
		totalWeight += k
	}
	return func() SimMsgFactoryX {
		// this is copied from old sims WeightedOperations.getSelectOpFn
		// TODO: refactor to make more efficient
		x := r.Intn(totalWeight)
		for i := 0; i < len(factories); i++ {
			if x <= int(factories[i].weight) {
				return factories[i].factory
			}
			x -= int(factories[i].weight)
		}
		// shouldn't happen
		return factories[0].factory
	}
}

func (s SimsV2Reg) Iterator() iter.Seq2[uint32, SimMsgFactoryX] {
	x := maps.Values(s)
	slices.SortFunc(x, func(a, b weightedFactory) int {
		return a.Compare(b)
	})
	return func(yield func(uint32, SimMsgFactoryX) bool) {
		for _, v := range x {
			if !yield(v.weight, v.factory) {
				return
			}
		}
	}
}

type weightedFactory struct {
	weight  uint32
	factory SimMsgFactoryX
}

func (f weightedFactory) Compare(b weightedFactory) int {
	switch {
	case f.weight > b.weight:
		return 1
	case f.weight < b.weight:
		return -1
	default:
		return strings.Compare(sdk.MsgTypeURL(f.factory.MsgType()), sdk.MsgTypeURL(b.factory.MsgType()))
	}
}
