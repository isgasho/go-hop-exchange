package hop

import (
	"context"
	"testing"
	"time"

	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	peer "github.com/libp2p/go-libp2p-core/peer"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/myelnet/go-hop-exchange/supply"
	"github.com/myelnet/go-hop-exchange/testutil"
	"github.com/myelnet/go-hop-exchange/wallet"
	"github.com/stretchr/testify/require"
)

func TestExchange(t *testing.T) {
	bgCtx := context.Background()

	ctx, cancel := context.WithTimeout(bgCtx, 10*time.Second)
	defer cancel()

	mn := mocknet.New(bgCtx)

	var client *Exchange
	var cnode *testutil.TestNode

	providers := make(map[peer.ID]*Exchange)
	pnodes := make(map[peer.ID]*testutil.TestNode)

	for i := 0; i < 11; i++ {
		n := testutil.NewTestNode(mn, t)
		n.SetupGraphSync(bgCtx)
		n.SetupTempRepo(t)
		ps, err := pubsub.NewGossipSub(bgCtx, n.Host)
		require.NoError(t, err)

		exch, err := NewExchange(
			bgCtx,
			WithBlockstore(n.Bs),
			WithPubSub(ps),
			WithHost(n.Host),
			WithDatastore(n.Ds),
			WithGraphSync(n.Gs),
			WithRepoPath(n.DTTmpDir),
			WithKeystore(wallet.NewMemKeystore()),
		)
		require.NoError(t, err)

		if i == 0 {
			client = exch
			cnode = n
		} else {
			providers[n.Host.ID()] = exch
			pnodes[n.Host.ID()] = n
		}
	}

	require.NoError(t, mn.LinkAll())

	require.NoError(t, mn.ConnectAllButSelf())

	link, origBytes := cnode.LoadUnixFSFileToStore(bgCtx, t, "/README.md")
	rootCid := link.(cidlink.Link).Cid

	done := make(chan bool, 1)
	unsubscribe := client.Supply().SubscribeToEvents(func(event supply.Event) {
		require.Equal(t, rootCid, event.PayloadCID)
		done <- true
	})
	defer unsubscribe()

	err := client.Announce(rootCid)
	require.NoError(t, err)

	select {
	case <-ctx.Done():
		t.Error("could not finish")
	case <-done:
		pp, err := client.Supply().ProviderPeersForContent(rootCid)
		require.NoError(t, err)
		for _, p := range pp {
			pnodes[p].VerifyFileTransferred(ctx, t, rootCid, origBytes)
		}
	}

	cnode.NukeBlockstore(ctx, t)

	// Sanity check to make sure our client does not have a copy of our blocks
	_, err = cnode.DAG.Get(ctx, rootCid)
	require.Error(t, err)

	// Now we fetch it again from our providers
	_, err = client.GetBlock(ctx, rootCid)
	require.NoError(t, err)

	// And we verify we got the file back
	cnode.VerifyFileTransferred(ctx, t, rootCid, origBytes)
}