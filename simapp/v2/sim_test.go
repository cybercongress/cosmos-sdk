package simapp

import (
	"context"
	addresscodec "cosmossdk.io/core/address"
	"cosmossdk.io/core/appmodule"
	appmodulev2 "cosmossdk.io/core/appmodule/v2"
	"cosmossdk.io/core/comet"
	corecontext "cosmossdk.io/core/context"
	"cosmossdk.io/core/server"
	"cosmossdk.io/core/store"
	"cosmossdk.io/core/transaction"
	"cosmossdk.io/depinject"
	"cosmossdk.io/log"
	"cosmossdk.io/runtime/v2"
	serverv2 "cosmossdk.io/server/v2"
	"cosmossdk.io/server/v2/appmanager"
	cometbfttypes "cosmossdk.io/server/v2/cometbft/types"
	consensustypes "cosmossdk.io/x/consensus/types"
	"encoding/json"
	"fmt"
	cmtproto "github.com/cometbft/cometbft/api/cometbft/types/v1"
	cmttypes "github.com/cometbft/cometbft/types"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/simsx"
	simsxv2 "github.com/cosmos/cosmos-sdk/simsx/v2"
	"github.com/cosmos/cosmos-sdk/std"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	"github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	simtypes "github.com/cosmos/cosmos-sdk/types/simulation"
	"github.com/cosmos/cosmos-sdk/x/simulation"
	"github.com/cosmos/cosmos-sdk/x/simulation/client/cli"
	"github.com/stretchr/testify/require"
	"golang.org/x/exp/maps"
	"iter"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	testing "testing"
	"time"
)

type Tx = transaction.Tx
type (
	HasWeightedOperationsX              = simsx.HasWeightedOperationsX
	HasWeightedOperationsXWithProposals = simsx.HasWeightedOperationsXWithProposals
	HasProposalMsgsX                    = simsx.HasProposalMsgsX
)

const (
	minTimePerBlock int64 = 10000 / 2

	maxTimePerBlock int64 = 10000

	timeRangePerBlock = maxTimePerBlock - minTimePerBlock
)

type AuthKeeper interface {
	simsx.ModuleAccountSource
	simsx.AccountSource
}

type BankKeeper interface {
	simsx.BalanceSource
	GetBlockedAddresses() map[string]bool
}

func TestSimsAppV2(t *testing.T) {
	DefaultNodeHome = t.TempDir()
	currentDir, err := os.Getwd()
	require.NoError(t, err)
	configPath := filepath.Join(currentDir, "testdata")
	v, err := serverv2.ReadConfig(configPath)
	require.NoError(t, err)
	v.Set("home", DefaultNodeHome)
	//v.Set("store.app-db-backend", "memdb") // todo: I had added this new type to speed up testing. Does it make sense this way?
	logger := log.NewTestLoggerInfo(t)
	app := NewSimApp[Tx](logger, v)

	var validatorAddressCodec addresscodec.ValidatorAddressCodec
	var addressCodec addresscodec.Codec
	var bankKeeper BankKeeper
	var authKeeper AuthKeeper
	err = depinject.Inject(
		depinject.Configs(
			AppConfig(),
			runtime.DefaultServiceBindings(),
			depinject.Supply(log.NewNopLogger()),
			depinject.Provide(
				codec.ProvideInterfaceRegistry,
				codec.ProvideAddressCodec,
				codec.ProvideProtoCodec,
				codec.ProvideLegacyAmino,
			),
			depinject.Invoke(
				std.RegisterInterfaces,
				std.RegisterLegacyAminoCodec,
			),
		),
		&authKeeper,
		&validatorAddressCodec,
		&addressCodec,
		&bankKeeper,
	)
	require.NoError(t, err)
	tCfg := cli.NewConfigFromFlags().With(t, 1, nil)
	r := rand.New(rand.NewSource(tCfg.Seed))
	modules := app.ModuleManager().Modules()
	accounts, genesisAppState, chainID, genesisTimestamp := prepareInitialGenesisState(app, addressCodec, validatorAddressCodec, modules, r, bankKeeper, tCfg)

	rootCtx, done := context.WithCancel(context.Background())
	defer done()

	txConfig := app.txConfig
	appManager := app.GetAppManager()
	appStore := app.GetStore().(cometbfttypes.Store)

	initRsp, stateRoot := doChainInitWithGenesis(t, rootCtx, chainID, genesisTimestamp, appManager, txConfig, genesisAppState, appStore)

	activeValidatorSet := simsxv2.NewValSet().Update(initRsp.ValidatorUpdates)
	valsetHistory := simsxv2.NewValSetHistory(150) // todo: configure
	valsetHistory.Add(genesisTimestamp, activeValidatorSet)

	emptySimParams := make(map[string]json.RawMessage) // todo read sims params from disk as before
	weights := simsx.ParamWeightSource(emptySimParams)

	msgFactoriesFn := prepareSimsMsgFactories(r, modules, weights)
	x := chainState[Tx]{
		chainID:            chainID,
		blockTime:          genesisTimestamp,
		activeValidatorSet: activeValidatorSet,
		valsetHistory:      valsetHistory,
		stateRoot:          stateRoot,
		app:                appManager,
		appStore:           appStore,
		txConfig:           txConfig,
	}
	doMainLoop(t, rootCtx, x, msgFactoriesFn, r, authKeeper, bankKeeper, accounts,
		func(ctx context.Context, ak simsx.AccountSource, senders []simsx.SimAccount, msg types.Msg, r *rand.Rand, txConfig client.TxConfig, chainID string) (Tx, error) {
			return simsxv2.BuildTestTX(ctx, ak, senders, msg, r, txConfig, chainID)
		})
}

func prepareInitialGenesisState[T Tx](
	app *SimApp[T],
	addressCodec addresscodec.Codec,
	validatorAddressCodec addresscodec.ValidatorAddressCodec,
	modules map[string]appmodulev2.AppModule,
	r *rand.Rand,
	bankKeeper BankKeeper,
	tCfg simtypes.Config,
) ([]simtypes.Account, json.RawMessage, string, time.Time) {
	// todo: replace legacy testdata functions
	appStateFn := simtestutil.AppStateFn(
		app.AppCodec(),
		addressCodec,
		validatorAddressCodec,
		toLegacySimsModule(modules),
		app.DefaultGenesis(),
	)
	params := simulation.RandomParams(r)
	accounts := slices.DeleteFunc(simtypes.RandomAccounts(r, params.NumKeys()),
		func(acc simtypes.Account) bool { // remove blocked accounts
			return bankKeeper.GetBlockedAddresses()[acc.AddressBech32]
		})

	appState, accounts, chainID, genesisTimestamp := appStateFn(r, accounts, tCfg)
	return accounts, appState, chainID, genesisTimestamp
}

func doChainInitWithGenesis[T Tx](
	t *testing.T,
	ctx context.Context,
	chainID string,
	genesisTimestamp time.Time,
	app *appmanager.AppManager[T],
	txConfig client.TxConfig,
	genesisAppState json.RawMessage,
	appStore cometbfttypes.Store,
) (*server.BlockResponse, store.Hash) {
	genesisReq := &server.BlockRequest[T]{
		Height:    0, // todo: or 1?
		Time:      genesisTimestamp,
		Hash:      make([]byte, 32),
		ChainId:   chainID,
		AppHash:   make([]byte, 32),
		IsGenesis: true,
	}

	initialConsensusParams := &consensustypes.MsgUpdateParams{
		Block: &cmtproto.BlockParams{
			MaxBytes: 200000,
			MaxGas:   100_000_000,
		},
		Evidence: &cmtproto.EvidenceParams{
			MaxAgeNumBlocks: 302400,
			MaxAgeDuration:  504 * time.Hour, // 3 weeks is the max duration
			MaxBytes:        10000,
		},
		Validator: &cmtproto.ValidatorParams{PubKeyTypes: []string{cmttypes.ABCIPubKeyTypeEd25519, cmttypes.ABCIPubKeyTypeSecp256k1}},
	}
	genesisCtx := context.WithValue(ctx, corecontext.CometParamsInitInfoKey, initialConsensusParams)

	initRsp, genesisStateChanges, err := app.InitGenesis(genesisCtx, genesisReq, genesisAppState, simsxv2.NewGenericTxDecoder[T](txConfig))
	require.NoError(t, err)

	require.NoError(t, appStore.SetInitialVersion(genesisReq.Height))
	changeSet, err := genesisStateChanges.GetStateChanges()
	require.NoError(t, err)

	stateRoot, err := appStore.Commit(&store.Changeset{Changes: changeSet})
	require.NoError(t, err)

	return initRsp, stateRoot
}

type chainState[T Tx] struct {
	chainID            string
	blockTime          time.Time
	activeValidatorSet simsxv2.WeightedValidators
	valsetHistory      *simsxv2.ValSetHistory
	stateRoot          store.Hash
	app                *appmanager.AppManager[T]
	appStore           cometbfttypes.Store
	txConfig           client.TxConfig
}

func doMainLoop[T Tx](
	t *testing.T,
	rootCtx context.Context,
	cs chainState[T],
	nextMsgFactory func() simsx.SimMsgFactoryX,
	r *rand.Rand,
	authKeeper AuthKeeper,
	bankKeeper simsx.BalanceSource,
	accounts []simtypes.Account,
	txBuilder txBuilder[T],
) {
	blockTime := cs.blockTime
	activeValidatorSet := cs.activeValidatorSet
	valsetHistory := cs.valsetHistory
	stateRoot := cs.stateRoot
	chainID := cs.chainID
	app := cs.app
	appStore := cs.appStore

	const ( // todo: read from CLI instead
		numBlocks     = 1200 // 500 default
		maxTXPerBlock = 650  // 200 default
	)

	var (
		txSkippedCounter int
		txTotalCounter   int
	)
	rootReporter := simsx.NewBasicSimulationReporter()
	futureOpsReg := simsxv2.NewFutureOpsRegistry()

	for i := 0; i < numBlocks; i++ {
		if len(activeValidatorSet) == 0 {
			t.Skipf("run out of validators in block: %d\n", i+1)
			return
		}
		blockTime = blockTime.Add(time.Duration(minTimePerBlock) * time.Second)
		blockTime = blockTime.Add(time.Duration(int64(r.Intn(int(timeRangePerBlock)))) * time.Second)
		valsetHistory.Add(blockTime, activeValidatorSet)
		blockReqN := &server.BlockRequest[T]{
			Height:  uint64(2 + i),
			Time:    blockTime,
			Hash:    stateRoot,
			AppHash: stateRoot,
			ChainId: chainID,
		}
		cometInfo := comet.Info{
			ValidatorsHash:  nil,
			Evidence:        valsetHistory.MissBehaviour(r),
			ProposerAddress: activeValidatorSet[0].Address,
			LastCommit:      activeValidatorSet.NewCommitInfo(r),
		}
		fOps, pos := futureOpsReg.FindScheduled(blockTime), 0
		nextFactoryFn := func() simsx.SimMsgFactoryX {
			if pos < len(fOps) {
				pos++
				return fOps[pos-1]
			}
			return nextMsgFactory()
		}
		simsCtx := context.WithValue(rootCtx, corecontext.CometInfoKey, cometInfo) // required for ContextAwareCometInfoService
		resultHandlers := make([]simsx.SimDeliveryResultHandler, 0, maxTXPerBlock)
		var txPerBlockCounter int
		blockRsp, updates, err := app.DeliverSims(simsCtx, blockReqN, func(ctx context.Context) iter.Seq[T] {
			return func(yield func(T) bool) {
				testData := simsx.NewChainDataSource(ctx, r, authKeeper, bankKeeper, cs.txConfig.SigningContext().AddressCodec(), accounts...)
				for txPerBlockCounter < maxTXPerBlock {
					txPerBlockCounter++
					msgFactory := nextFactoryFn()
					reporter := rootReporter.WithScope(msgFactory.MsgType())
					if fx, ok := msgFactory.(simsx.HasFutureOpsRegistry); ok {
						fx.SetFutureOpsRegistry(futureOpsReg)
					}

					// the stf context is required to access state via keepers
					signers, msg := msgFactory.Create()(ctx, testData, reporter)
					if reporter.IsSkipped() {
						txSkippedCounter++
						require.NoError(t, reporter.Close())
						continue
					}
					resultHandlers = append(resultHandlers, msgFactory.DeliveryResultHandler())
					reporter.Success(msg)
					require.NoError(t, reporter.Close())

					tx, err := txBuilder(ctx, authKeeper, signers, msg, r, cs.txConfig, chainID)
					require.NoError(t, err)
					if !yield(tx) {
						return
					}
				}
			}
		})
		require.NoError(t, err)
		changeSet, err := updates.GetStateChanges()
		require.NoError(t, err)
		stateRoot, err = appStore.Commit(&store.Changeset{Changes: changeSet})
		require.NoError(t, err)
		require.Equal(t, len(resultHandlers), len(blockRsp.TxResults), "txPerBlockCounter: %d, totalSkipped: %d", txPerBlockCounter, txSkippedCounter)
		for i, v := range blockRsp.TxResults {
			require.NoError(t, resultHandlers[i](v.Error))
		}
		txTotalCounter += txPerBlockCounter
		activeValidatorSet = activeValidatorSet.Update(blockRsp.ValidatorUpdates)
		fmt.Printf("active validator set: %d\n", len(activeValidatorSet))
	}
	fmt.Println("+++ reporter:\n" + rootReporter.Summary().String())
	fmt.Printf("Tx total: %d skipped: %d\n", txTotalCounter, txSkippedCounter)
}

func prepareSimsMsgFactories(
	r *rand.Rand,
	modules map[string]appmodulev2.AppModule,
	weights simsx.WeightSource,
) func() simsx.SimMsgFactoryX {
	// get all proposal types
	proposalRegistry := simsx.NewUniqueTypeRegistry()
	for _, m := range modules {
		switch xm := m.(type) {
		case HasProposalMsgsX:
			xm.ProposalMsgsX(weights, proposalRegistry)
			// todo: register legacy and v1 msg proposals
		}
	}
	// register all msg factories
	factoryRegistry := simsx.NewUnorderedRegistry()
	for _, m := range modules {
		switch xm := m.(type) {
		case HasWeightedOperationsX:
			xm.WeightedOperationsX(weights, factoryRegistry)
		case HasWeightedOperationsXWithProposals:
			xm.WeightedOperationsX(weights, factoryRegistry, proposalRegistry.Iterator(), nil)
		}
	}
	msgFactoriesFn := simsxv2.NextFactoryFn(*factoryRegistry, r)
	return msgFactoriesFn
}

func toLegacySimsModule(modules map[string]appmodule.AppModule) []module.AppModuleSimulation {
	r := make([]module.AppModuleSimulation, 0, len(modules))
	names := maps.Keys(modules)
	slices.Sort(names) // make deterministic
	for _, v := range names {
		if m, ok := modules[v].(module.AppModuleSimulation); ok {
			r = append(r, m)
		}
	}
	return r
}

type txBuilder[T Tx] func(ctx context.Context,
	ak simsx.AccountSource,
	senders []simsx.SimAccount,
	msg types.Msg,
	r *rand.Rand,
	txGen client.TxConfig,
	chainID string,
) (T, error)
