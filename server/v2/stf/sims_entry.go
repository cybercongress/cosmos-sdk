package stf

import (
	"context"
	"cosmossdk.io/core/header"
	"cosmossdk.io/core/server"
	"cosmossdk.io/core/store"
	"cosmossdk.io/core/transaction"
	"iter"
)

func (s STF[T]) DoSimsTXs(simsBuilder func(ctx context.Context) iter.Seq[T]) doInBlockDeliveryFn[T] {
	return func(
		ctx context.Context,
		_ []T,
		newState store.WriterMap,
		hi header.Info,
	) ([]server.TxResult, error) {
		simsCtx := context.WithValue(ctx, "sims.header.time", hi.Time) // using string key to decouple
		var results []server.TxResult
		for tx := range simsBuilder(simsCtx) {
			if err := isCtxCancelled(simsCtx); err != nil {
				return nil, err
			}
			results = append(results, s.deliverTx(simsCtx, newState, tx, transaction.ExecModeFinalize, hi))
		}
		return results, nil
	}
}
