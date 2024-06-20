package listener

import (
	"context"

	"cosmossdk.io/schema"
)

// Listener is an interface that defines methods for listening to both raw and logical blockchain data.
// It is valid for any of the methods to be nil, in which case the listener will not be called for that event.
// Listeners should understand the guarantees that are provided by the source they are listening to and
// understand which methods will or will not be called. For instance, most blockchains will not do logical
// decoding of data out of the box, so the InitializeModuleSchema and OnObjectUpdate methods will not be called.
// These methods will only be called when listening logical decoding is setup.
type Listener struct {
	// Initialize is called when the listener is initialized before any other methods are called.
	// The lastBlockPersisted return value should be the last block height the listener persisted if it is
	// persisting block data, 0 if it is not interested in persisting block data, or -1 if it is
	// persisting block data but has not persisted any data yet. This check allows the indexer
	// framework to ensure that the listener has not missed blocks. Data sources MUST call
	// initialize before any other method is called, otherwise, no data will be processed.
	Initialize func(context.Context, InitializationData) (lastBlockPersisted int64, err error)

	// InitializeModuleSchema should be called whenever the blockchain process starts OR whenever
	// logical decoding of a module is initiated. An indexer listening to this event
	// should ensure that they have performed whatever initialization steps (such as database
	// migrations) required to receive OnObjectUpdate events for the given module. If the
	// indexer's schema is incompatible with the module's on-chain schema, the listener should return
	// an error. Module names must conform to the NameFormat regular expression.
	InitializeModuleSchema func(moduleName string, moduleSchema schema.ModuleSchema) error

	CompleteInitialization func() error

	// StartBlock is called at the beginning of processing a block.
	StartBlock func(uint64) error

	// OnBlockHeader is called when a block header is received.
	OnBlockHeader func(BlockHeaderData) error

	// OnTx is called when a transaction is received.
	OnTx func(TxData) error

	// OnEvent is called when an event is received.
	OnEvent func(EventData) error

	// OnKVPair is called when a key-value has been written to the store for a given module.
	// Module names must conform to the NameFormat regular expression.
	OnKVPair func(KVPairData) error

	// OnObjectUpdate is called whenever an object is updated in a module's state. This is only called
	// when logical data is available. It should be assumed that the same data in raw form
	// is also passed to OnKVPair. Module names must conform to the NameFormat regular expression.
	OnObjectUpdate func(ObjectUpdateData) error

	// Commit is called when state is committed, usually at the end of a block. Any
	// indexers should commit their data when this is called and return an error if
	// they are unable to commit. Data sources MUST call Commit when data is committed,
	// otherwise it should be assumed that indexers have not persisted their state.
	Commit func() error
}
