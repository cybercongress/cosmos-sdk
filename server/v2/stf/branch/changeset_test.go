package branch

import (
	"bytes"
	corestore "cosmossdk.io/core/store"
	"reflect"
	"testing"
)

func TestChangeSet_Next(t *testing.T) {
	tests := map[string]struct {
		setup    func() corestore.Iterator
		expected []string // expected keys sequence
	}{
		"both iterators are empty": {
			setup: func() corestore.Iterator {
				parent := newChangeSet()
				cache := newChangeSet()
				return mergeIterators(must(parent.iterator(nil, nil)), must(cache.iterator(nil, nil)), true)
			},
		},
		"parent iterator has one item, cache is empty": {
			setup: func() corestore.Iterator {
				parent := newChangeSet()
				parent.set([]byte("k1"), []byte("1"))
				cache := newChangeSet()
				return mergeIterators(must(parent.iterator(nil, nil)), must(cache.iterator(nil, nil)), true)
			},
			expected: []string{"k1"},
		},
		"cache has one item, parent is empty": {
			setup: func() corestore.Iterator {
				parent := newChangeSet()
				cache := newChangeSet()
				cache.set([]byte("k1"), []byte("1"))
				return mergeIterators(must(parent.iterator(nil, nil)), must(cache.iterator(nil, nil)), true)
			},
			expected: []string{"k1"},
		},
		"both iterators have items, but cache value is nil": {
			setup: func() corestore.Iterator {
				parent := newChangeSet()
				parent.set([]byte("k1"), []byte("1"))
				cache := newChangeSet()
				cache.set([]byte("k1"), nil)
				return mergeIterators(must(parent.iterator(nil, nil)), must(cache.iterator(nil, nil)), true)
			},
		},
		"parent and cache are ascending": {
			setup: func() corestore.Iterator {
				parent := newChangeSet()
				parent.set([]byte("k2"), []byte("v2"))
				parent.set([]byte("k3"), []byte("v3"))
				cache := newChangeSet()
				cache.set([]byte("k1"), []byte("v1"))
				cache.set([]byte("k4"), []byte("v4"))
				return mergeIterators(must(parent.iterator(nil, nil)), must(cache.iterator(nil, nil)), true)
			},
			expected: []string{"k1", "k2", "k3", "k4"},
		},
		"parent and cache are descending": {
			setup: func() corestore.Iterator {
				parent := newChangeSet()
				parent.set([]byte("k3"), []byte("v3"))
				parent.set([]byte("k2"), []byte("v2"))
				cache := newChangeSet()
				cache.set([]byte("k4"), []byte("v4"))
				cache.set([]byte("k1"), []byte("v1"))
				return mergeIterators(must(parent.reverseIterator(nil, nil)), must(cache.reverseIterator(nil, nil)), false)
			},
			expected: []string{"k4", "k3", "k2", "k1"},
		},
	}
	for name, spec := range tests {
		t.Run(name, func(t *testing.T) {
			var got []string
			for iter := spec.setup(); iter.Valid(); iter.Next() {
				got = append(got, string(iter.Key()))
			}
			if !reflect.DeepEqual(spec.expected, got) {
				t.Errorf("expected: %#v, got: %#v", spec.expected, got)
			}
		})
	}
}

func TestBreakBtree(t *testing.T) {
	myPrefix, otherPrefix := byte(0x01), byte(0x02)
	parent := newChangeSet()
	for i := byte(0); i < 63; i++ { // set to 63 elements to have a node split on the next insert
		parent.set([]byte{myPrefix, i}, []byte{i})
	}
	it, _ := parent.reverseIterator([]byte{myPrefix, 32}, []byte{myPrefix, 34}) // ValidatorsPowerStoreIterator
	if !it.Valid() {
		t.Fatal("expected valid iterator")
	}
	// when btree is modified on a non conflicting key
	parent.set([]byte{otherPrefix, byte(1)}, []byte("any value")) // SetLastValidatorPower

	// then
	var got [][2][]byte
	for ; it.Valid(); it.Next() {
		t.Logf("got key: %x\n", it.Key())
		got = append(got, [2][]byte{it.Key(), it.Value()})
	}
	exp := [][2][]byte{
		{{myPrefix, byte(33)}, []byte{33}},
		{{myPrefix, byte(32)}, []byte{32}},
	}
	if !reflect.DeepEqual(exp, got) {
		t.Errorf("expected %#v, got %#v", exp, got)
	}
	// and when new data is stored
	parent.set([]byte{otherPrefix, byte(2)}, []byte("other value"))
	// then
	gotVal, ok := parent.get([]byte{otherPrefix, byte(2)})
	if !ok {
		t.Fatal("not found")
	}
	expVal := []byte("other value")
	if bytes.Compare(gotVal, expVal) != 0 {
		t.Errorf("expected %#v, got %#v", expVal, gotVal)
	}

}
