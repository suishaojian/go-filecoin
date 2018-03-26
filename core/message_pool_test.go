package core

import (
	"context"
	"fmt"
	hamt "gx/ipfs/QmdtiofXbibTe6Day9ii5zjBZpSRm8vhfoerrNuY3sAQ7e/go-hamt-ipld"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/filecoin-project/go-filecoin/types"
)

func TestMessagePoolAddRemove(t *testing.T) {
	assert := assert.New(t)
	newMsg := types.NewMessageForTestGetter()

	pool := NewMessagePool()
	msg1 := newMsg()
	msg2 := newMsg()

	c1, err := msg1.Cid()
	assert.NoError(err)
	c2, err := msg2.Cid()
	assert.NoError(err)

	assert.Len(pool.Pending(), 0)
	_, err = pool.Add(msg1)
	assert.NoError(err)
	assert.Len(pool.Pending(), 1)
	_, err = pool.Add(msg2)
	assert.NoError(err)
	assert.Len(pool.Pending(), 2)

	pool.Remove(c1)
	assert.Len(pool.Pending(), 1)
	pool.Remove(c2)
	assert.Len(pool.Pending(), 0)
}

func TestMessagePoolDedup(t *testing.T) {
	assert := assert.New(t)

	pool := NewMessagePool()
	msg1 := types.NewMessageForTestGetter()()

	assert.Len(pool.Pending(), 0)
	_, err := pool.Add(msg1)
	assert.NoError(err)
	assert.Len(pool.Pending(), 1)

	_, err = pool.Add(msg1)
	assert.NoError(err)
	assert.Len(pool.Pending(), 1)
}

func TestMessagePoolAsync(t *testing.T) {
	assert := assert.New(t)

	count := 400
	msgs := make([]*types.Message, count)
	addrGetter := types.NewAddressForTestGetter()

	for i := 0; i < count; i++ {
		msgs[i] = types.NewMessage(
			addrGetter(),
			addrGetter(),
			types.NewTokenAmount(1),
			"send",
			nil,
		)
	}

	pool := NewMessagePool()
	var wg sync.WaitGroup

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			for j := 0; j < count/4; j++ {
				_, err := pool.Add(msgs[j+(count/4)*i])
				assert.NoError(err)
			}
			wg.Done()
		}(i)
	}

	wg.Wait()
	assert.Len(pool.Pending(), count)
}

func msgAsString(msg *types.Message) string {
	// When using NewMessageForTestGetter msg.Method is set
	// to "msgN" so we print that (it will correspond
	// to a variable of the same name in the tests
	// below).
	return msg.Method
}

func msgsAsString(msgs []*types.Message) string {
	s := ""
	for _, m := range msgs {
		s = fmt.Sprintf("%s%s ", s, msgAsString(m))
	}
	return "[" + s + "]"
}

// assertPoolEquals returns true if p contains exactly the expected messages.
func assertPoolEquals(assert *assert.Assertions, p *MessagePool, expMsgs ...*types.Message) {
	msgs := p.Pending()
	if len(msgs) != len(expMsgs) {
		assert.Failf("wrong messages in pool", "expMsgs %v, got msgs %v", msgsAsString(expMsgs), msgsAsString(msgs))

	}
	for _, m1 := range expMsgs {
		found := false
		for _, m2 := range msgs {
			if types.MsgCidsEqual(m1, m2) {
				found = true
				break
			}
		}
		if !found {
			assert.Failf("wrong messages in pool", "expMsgs %v, got msgs %v (msgs doesn't contain %v)", msgsAsString(expMsgs), msgsAsString(msgs), msgAsString(m1))
		}
	}
}

func headOf(chain []*types.Block) *types.Block {
	return chain[len(chain)-1]
}

func TestUpdateMessagePool(t *testing.T) {
	assert := assert.New(t)
	ctx := context.Background()
	type msgs []*types.Message

	t.Run("Replace head", func(t *testing.T) {
		// Msg pool: [m0, m1], Chain: b[]
		// to
		// Msg pool: [m0],     Chain: b[m1]
		store := hamt.NewCborStore()
		p := NewMessagePool()
		m := types.NewMsgs(2)
		MustAdd(p, m[0], m[1])
		oldChain := NewChainWithMessages(store, nil, msgs{})
		newChain := NewChainWithMessages(store, nil, msgs{m[1]})
		assert.NoError(UpdateMessagePool(ctx, p, store, headOf(oldChain), headOf(newChain)))
		assertPoolEquals(assert, p, m[0])
	})

	t.Run("Replace head with self", func(t *testing.T) {
		// Msg pool: [m0, m1], Chain: b[m2]
		// to
		// Msg pool: [m0, m1], Chain: b[m2]
		store := hamt.NewCborStore()
		p := NewMessagePool()
		m := types.NewMsgs(3)
		MustAdd(p, m[0], m[1])
		oldChain := NewChainWithMessages(store, nil, msgs{m[2]})
		UpdateMessagePool(ctx, p, store, headOf(oldChain), headOf(oldChain)) // sic
		assertPoolEquals(assert, p, m[0], m[1])
	})

	t.Run("Replace head with a long chain", func(t *testing.T) {
		// Msg pool: [m2, m5],     Chain: b[m0, m12]
		// to
		// Msg pool: [m1],         Chain: b[m2, m3] -> b[m4] -> b[m0] -> b[] -> b[m5, m6]
		store := hamt.NewCborStore()
		p := NewMessagePool()
		m := types.NewMsgs(7)
		MustAdd(p, m[2], m[5])
		oldChain := NewChainWithMessages(store, nil, msgs{m[0], m[1]})
		newChain := NewChainWithMessages(store, nil, msgs{m[2], m[3]}, msgs{m[4]}, msgs{m[0]}, msgs{}, msgs{m[5], m[6]})
		UpdateMessagePool(ctx, p, store, headOf(oldChain), headOf(newChain))
		assertPoolEquals(assert, p, m[1])
	})

	t.Run("Replace internal node (second one)", func(t *testing.T) {
		// Msg pool: [m3, m5],     Chain: b[m0] -> b[m1] -> b[m2]
		// to
		// Msg pool: [m1, m2],     Chain: b[m0] -> b[m3] -> b[m4, m5]
		store := hamt.NewCborStore()
		p := NewMessagePool()
		m := types.NewMsgs(6)
		MustAdd(p, m[3], m[5])
		oldChain := NewChainWithMessages(store, nil, msgs{m[0]}, msgs{m[1]}, msgs{m[2]})
		newChain := NewChainWithMessages(store, oldChain[0], msgs{m[3]}, msgs{m[4], m[5]})
		UpdateMessagePool(ctx, p, store, headOf(oldChain), headOf(newChain))
		assertPoolEquals(assert, p, m[1], m[2])
	})

	t.Run("Replace internal node (second one) with a long chain", func(t *testing.T) {
		// Msg pool: [m6],         Chain: b[m0] -> b[m1] -> b[m2]
		// to
		// Msg pool: [m6],         Chain: b[m0] -> b[m3] -> b[m4] -> b[m5] -> b[m1, m2]
		store := hamt.NewCborStore()
		p := NewMessagePool()
		m := types.NewMsgs(7)
		MustAdd(p, m[6])
		oldChain := NewChainWithMessages(store, nil, msgs{m[0]}, msgs{m[1]}, msgs{m[2]})
		newChain := NewChainWithMessages(store, oldChain[0], msgs{m[3]}, msgs{m[4]}, msgs{m[5]}, msgs{m[1], m[2]})
		UpdateMessagePool(ctx, p, store, headOf(oldChain), headOf(newChain))
		assertPoolEquals(assert, p, m[6])
	})

	t.Run("Truncate to internal node", func(t *testing.T) {
		// Msg pool: [],               Chain: b[m0] -> b[m1] -> b[m2] -> b[m3]
		// to
		// Msg pool: [m2, m3],         Chain: b[m0] -> b[m1]
		store := hamt.NewCborStore()
		p := NewMessagePool()
		m := types.NewMsgs(4)
		oldChain := NewChainWithMessages(store, nil, msgs{m[0]}, msgs{m[1]}, msgs{m[2]}, msgs{m[3]})
		UpdateMessagePool(ctx, p, store, headOf(oldChain), oldChain[1])
		assertPoolEquals(assert, p, m[2], m[3])
	})

	t.Run("Extend head", func(t *testing.T) {
		// Msg pool: [m0, m1], Chain: b[]
		// to
		// Msg pool: [m0],     Chain: b[] -> b[m1, m2]
		store := hamt.NewCborStore()
		p := NewMessagePool()
		m := types.NewMsgs(3)
		MustAdd(p, m[0], m[1])
		oldChain := NewChainWithMessages(store, nil, msgs{})
		newChain := NewChainWithMessages(store, oldChain[len(oldChain)-1], msgs{m[1], m[2]})
		UpdateMessagePool(ctx, p, store, headOf(oldChain), headOf(newChain))
		assertPoolEquals(assert, p, m[0])
	})

	t.Run("Extend head with a longer chain and more messages", func(t *testing.T) {
		// Msg pool: [m2, m5],     Chain: b[m0] -> b[m1]
		// to
		// Msg pool: [],           Chain: b[m0] -> b[m1] -> b[m2, m3] -> b[m4] -> b[m5, m6]
		store := hamt.NewCborStore()
		p := NewMessagePool()
		m := types.NewMsgs(7)
		MustAdd(p, m[2], m[5])
		oldChain := NewChainWithMessages(store, nil, msgs{m[0]}, msgs{m[1]})
		newChain := NewChainWithMessages(store, oldChain[1], msgs{m[2], m[3]}, msgs{m[4]}, msgs{m[5], m[6]})
		UpdateMessagePool(ctx, p, store, headOf(oldChain), headOf(newChain))
		assertPoolEquals(assert, p)
	})
}