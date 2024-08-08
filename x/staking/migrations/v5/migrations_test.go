package v5_test

import (
	"math"
	"math/rand"
	"testing"
	"time"

	cmtproto "github.com/cometbft/cometbft/api/cometbft/types/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corestore "cosmossdk.io/core/store"
	coretesting "cosmossdk.io/core/testing"
	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	"cosmossdk.io/x/staking"
	v5 "cosmossdk.io/x/staking/migrations/v5"
	stakingtypes "cosmossdk.io/x/staking/types"

	"github.com/cosmos/cosmos-sdk/codec"
	codectestutil "github.com/cosmos/cosmos-sdk/codec/testutil"
	"github.com/cosmos/cosmos-sdk/runtime"
	"github.com/cosmos/cosmos-sdk/testutil"
	"github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"
	moduletestutil "github.com/cosmos/cosmos-sdk/types/module/testutil"
)

func TestHistoricalKeysMigration(t *testing.T) {
	ctx := testutil.DefaultContext("staking")
	storeService := coretesting.KVStoreService(ctx, "staking")
	store := storeService.OpenKVStore(ctx)
	logger := coretesting.NewNopLogger()

	type testCase struct {
		oldKey, newKey []byte
		historicalInfo []byte
	}

	testCases := make(map[int64]testCase)

	// edge cases
	testCases[0], testCases[1], testCases[math.MaxInt32] = testCase{}, testCase{}, testCase{}

	// random cases
	seed := time.Now().UnixNano()
	r := rand.New(rand.NewSource(seed))
	for i := 0; i < 10; i++ {
		height := r.Intn(math.MaxInt32-2) + 2

		testCases[int64(height)] = testCase{}
	}

	cdc := moduletestutil.MakeTestEncodingConfig(codectestutil.CodecOptions{}).Codec
	for height := range testCases {
		testCases[height] = testCase{
			oldKey:         v5.GetLegacyHistoricalInfoKey(height),
			newKey:         v5.GetHistoricalInfoKey(height),
			historicalInfo: cdc.MustMarshal(createHistoricalInfo(height, "testChainID")),
		}
	}

	// populate store using old key format
	for _, tc := range testCases {
		err := store.Set(tc.oldKey, tc.historicalInfo)
		require.NoError(t, err)
	}

	// migrate store to new key format
	require.NoErrorf(t, v5.MigrateStore(ctx, store, cdc, logger), "v5.MigrateStore failed, seed: %d", seed)

	// check results
	for _, tc := range testCases {
		bz, err := store.Get(tc.oldKey)
		require.NoError(t, err)
		require.Nilf(t, bz, "old key should be deleted, seed: %d", seed)

		bz, err = store.Get(tc.newKey)
		require.NoError(t, err)
		require.NotNilf(t, bz, "new key should be created, seed: %d", seed)
		require.Equalf(t, tc.historicalInfo, bz, "seed: %d", seed)
	}
}

func createHistoricalInfo(height int64, chainID string) *stakingtypes.HistoricalInfo {
	return &stakingtypes.HistoricalInfo{Header: cmtproto.Header{ChainID: chainID, Height: height}}
}

func TestDelegationsByValidatorMigrations(t *testing.T) {
	codecOpts := codectestutil.CodecOptions{}
	cdc := moduletestutil.MakeTestEncodingConfig(codecOpts, staking.AppModule{}).Codec

	ctx := testutil.DefaultContext(v5.ModuleName)
	storeService := coretesting.KVStoreService(ctx, v5.ModuleName)
	store := storeService.OpenKVStore(ctx)
	logger := coretesting.NewNopLogger()

	accAddrs := sims.CreateIncrementalAccounts(11)
	valAddrs := sims.ConvertAddrsToValAddrs(accAddrs[0:1])
	var addedDels []stakingtypes.Delegation

	valAddr, err := codecOpts.GetValidatorCodec().BytesToString(valAddrs[0])
	assert.NoError(t, err)

	for i := 1; i < 11; i++ {
		accAddr, err := codecOpts.GetAddressCodec().BytesToString(accAddrs[i])
		assert.NoError(t, err)
		del1 := stakingtypes.NewDelegation(accAddr, valAddr, sdkmath.LegacyNewDec(100))
		err = store.Set(v5.GetDelegationKey(accAddrs[i], valAddrs[0]), stakingtypes.MustMarshalDelegation(cdc, del1))
		require.NoError(t, err)
		addedDels = append(addedDels, del1)
	}

	// before migration the state of delegations by val index should be empty
	dels := getValDelegations(ctx, cdc, storeService, valAddrs[0])
	assert.Len(t, dels, 0)

	err = v5.MigrateStore(ctx, store, cdc, logger)
	assert.NoError(t, err)

	// after migration the state of delegations by val index should not be empty
	dels = getValDelegations(ctx, cdc, storeService, valAddrs[0])
	assert.Len(t, dels, len(addedDels))
	assert.Equal(t, addedDels, dels)
}

func getValDelegations(ctx sdk.Context, cdc codec.Codec, storeService corestore.KVStoreService, valAddr sdk.ValAddress) []stakingtypes.Delegation {
	var delegations []stakingtypes.Delegation

	iterator := storetypes.KVStorePrefixIterator(runtime.KVStoreAdapter(storeService.OpenKVStore(ctx)), v5.GetDelegationsByValPrefixKey(valAddr))
	for ; iterator.Valid(); iterator.Next() {
		var delegation stakingtypes.Delegation
		valAddr, delAddr, err := v5.ParseDelegationsByValKey(iterator.Key())
		if err != nil {
			panic(err)
		}

		bz, err := storeService.OpenKVStore(ctx).Get(v5.GetDelegationKey(delAddr, valAddr))
		if err != nil {
			panic(err)
		}

		cdc.MustUnmarshal(bz, &delegation)

		delegations = append(delegations, delegation)
	}

	return delegations
}
