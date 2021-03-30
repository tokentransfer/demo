package node

import (
	"errors"
	"fmt"
	"time"

	"github.com/tokentransfer/chain/block"
	"github.com/tokentransfer/chain/core"

	libaccount "github.com/tokentransfer/interfaces/account"
	libblock "github.com/tokentransfer/interfaces/block"
	libcore "github.com/tokentransfer/interfaces/core"
	libcrypto "github.com/tokentransfer/interfaces/crypto"
	libstore "github.com/tokentransfer/interfaces/store"
)

type ConsensusService struct {
	CryptoService  libcrypto.CryptoService
	MerkleService  libstore.MerkleService
	Config         libcore.Config
	AccountService libaccount.AccountService

	ValidatedBlock libblock.Block
	CurrentBlock   libblock.Block
}

func (service *ConsensusService) GetBlockNumber() int64 {
	if service.ValidatedBlock != nil {
		return int64(service.ValidatedBlock.GetIndex())
	}
	return -1
}

func (service *ConsensusService) GenerateBlock(list []libblock.TransactionWithData) (libblock.Block, error) {
	config := service.Config
	cs := service.CryptoService
	as := service.AccountService

	var b *block.Block
	if service.ValidatedBlock == nil { //genesis
		if len(list) > 0 {
			return nil, errors.New("error genesis block")
		}

		_, rootKey, err := as.GenerateFamilySeed("masterpassphrase")
		if err != nil {
			return nil, err
		}
		rootAccount, err := rootKey.GetAddress()
		if err != nil {
			return nil, err
		}
		a, err := core.NewAmount("100000000000000")
		if err != nil {
			return nil, err
		}
		states := []libblock.State{
			&block.CurrencyState{
				State: block.State{
					StateType:  block.CURRENCY_STATE,
					Account:    rootAccount,
					Sequence:   uint64(0),
					BlockIndex: uint64(0),
				},
				Name:        fmt.Sprintf("%s Coin", config.GetSystemCode()),
				Symbol:      config.GetSystemCode(),
				Decimals:    6,
				TotalSupply: *a,
			},
			&block.AccountState{
				State: block.State{
					StateType:  block.ACCOUNT_STATE,
					Account:    rootAccount,
					Sequence:   uint64(0),
					BlockIndex: uint64(0),
				},
				Amount: *a,
			},
		}

		ms := service.MerkleService
		for i := 0; i < len(states); i++ {
			state := states[i]
			err := ms.PutState(state)
			if err != nil {
				return nil, err
			}
		}

		b = &block.Block{
			BlockIndex: uint64(0),
			ParentHash: libcrypto.ZeroHash(cs),

			Transactions:    []libblock.TransactionWithData{},
			TransactionHash: ms.GetTransactionRoot(),

			States:    states,
			StateHash: ms.GetStateRoot(),

			Timestamp: time.Now().UnixNano(),
		}

		err = ms.Cancel()
		if err != nil {
			return nil, err
		}
	} else {
		ms := service.MerkleService
		v := service.ValidatedBlock

		fmt.Printf("=== package %d transactions in block %d\n", len(list), v.GetIndex()+1)

		stateMap := map[string][]uint64{}
		for i := 0; i < len(list); i++ {
			txWithData := list[i]

			r := txWithData.GetReceipt()
			r.SetTransactionIndex(uint32(i))
			r.SetBlockIndex(v.GetIndex() + 1)
			states := r.GetStates()
			for j := 0; j < len(states); j++ {
				s := states[j]
				s.SetBlockIndex(v.GetIndex() + 1)

				key := fmt.Sprintf("%d-%s", s.GetStateType(), s.GetStateKey())
				index := s.GetIndex()
				stateMap[key] = []uint64{uint64(i), index}
			}

			err := ms.PutTransaction(txWithData)
			if err != nil {
				return nil, err
			}

			fmt.Printf("=== %d %s\n", i, txWithData.GetTransaction().GetHash().String())
		}

		states := make([]libblock.State, 0)
		for i := 0; i < len(list); i++ {
			txWithData := list[i]

			r := txWithData.GetReceipt()
			rs := r.GetStates()
			for j := 0; j < len(rs); j++ {
				s := rs[j]

				key := fmt.Sprintf("%d-%s", s.GetStateType(), s.GetStateKey())
				item, ok := stateMap[key]
				if ok && item[0] == uint64(i) && item[1] == s.GetIndex() {
					states = append(states, s)
				}
			}
		}

		for i := 0; i < len(states); i++ {
			state := states[i]
			err := service.MerkleService.PutState(state)
			if err != nil {
				return nil, err
			}
		}

		b = &block.Block{
			BlockIndex: v.GetIndex() + 1,
			ParentHash: v.GetHash(),

			Transactions:    list,
			TransactionHash: ms.GetTransactionRoot(),

			States:    states,
			StateHash: ms.GetStateRoot(),

			Timestamp: time.Now().UnixNano(),
		}

		err := ms.Cancel()
		if err != nil {
			return nil, err
		}
	}

	_, _, err := cs.Raw(b, libcrypto.RawBinary)
	if err != nil {
		return nil, err
	}

	return b, nil
}

func (service *ConsensusService) VerifyBlock(b libblock.Block) (ok bool, err error) {
	ms := service.MerkleService
	cs := service.CryptoService

	ok = true
	err = nil

	defer func() {
		if !ok || err != nil {
			ms.Cancel()
		}
	}()

	transactions := b.GetTransactions()
	l := len(transactions)
	for i := 0; i < l; i++ {
		txWithData := transactions[i]
		tx := txWithData.GetTransaction()

		ok, err = service.VerifyTransaction(tx)
		if err != nil {
			return
		}
		if !ok {
			err = errors.New("verify transaction failed")
			return
		}

		newWithData, e := service.ProcessTransaction(tx)
		if e != nil {
			ok = false
			err = e
			return
		}

		arh, _, e := cs.Raw(txWithData.GetReceipt(), libcrypto.RawBinary)
		if e != nil {
			ok = false
			err = e
			return
		}
		brh, _, e := cs.Raw(newWithData.GetReceipt(), libcrypto.RawBinary)
		if e != nil {
			ok = false
			err = e
			return
		}
		if !arh.Equals(brh) {
			ok = false
			err = errors.New("process transaction receipt failed")
			return
		}

		err = ms.PutTransaction(txWithData)
		if err != nil {
			ok = false
			return
		}
	}

	if service.ValidatedBlock != nil {
		if b.GetIndex() != (service.ValidatedBlock.GetIndex() + 1) {
			ok = false
			err = fmt.Errorf("error block index: %d != %d", b.GetIndex(), (service.ValidatedBlock.GetIndex() + 1))
			return
		}
		if !b.GetParentHash().Equals(service.ValidatedBlock.GetHash()) {
			ok = false
			err = fmt.Errorf("error parent hash: %s != %s", b.GetParentHash().String(), service.ValidatedBlock.GetHash().String())
			return
		}
	} else {
		if b.GetIndex() != 0 {
			ok = false
			err = errors.New("error block index")
			return
		}
		if !b.GetParentHash().IsZero() {
			ok = false
			err = errors.New("error parent hash")
			return
		}
	}

	states := b.GetStates()
	l = len(states)
	for i := 0; i < l; i++ {
		state := states[i]
		err = ms.PutState(state)
		if err != nil {
			ok = false
			return
		}
	}

	transactionHash := ms.GetTransactionRoot()
	stateHash := ms.GetStateRoot()
	if !b.GetTransactionHash().Equals(transactionHash) {
		ok = false
		err = fmt.Errorf("error transaction hash: %s != %s", b.GetTransactionHash().String(), transactionHash.String())
		return
	}
	if !b.GetStateHash().Equals(stateHash) {
		ok = false
		err = fmt.Errorf("error state hash: %s != %s", b.GetStateHash().String(), stateHash.String())
		return
	}
	return
}

func (service *ConsensusService) AddBlock(b libblock.Block) error {
	ms := service.MerkleService

	err := ms.PutBlock(b)
	if err != nil {
		return err
	}
	err = ms.Commit()
	if err != nil {
		return err
	}
	service.ValidatedBlock = b

	return nil
}

func (service *ConsensusService) GetAccountInfo(account libcore.Address, currency *libcore.Symbol, issuer libcore.Address) (*block.AccountState, error) {
	ms := service.MerkleService

	accountKey := core.GetAccountKey(account, currency, issuer, "-")
	state, err := ms.GetStateByTypeAndKey(block.ACCOUNT_STATE, accountKey)
	if err != nil {
		return nil, err
	}
	info, ok := state.(*block.AccountState)
	if !ok {
		return nil, errors.New("error account state")
	}
	return info, nil
}

func (service *ConsensusService) VerifyTransaction(t libblock.Transaction) (bool, error) {
	cs := service.CryptoService
	ms := service.MerkleService

	ok, err := cs.Verify(t)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, errors.New("error transaction")
	}
	tx, ok := t.(*block.Transaction)
	if !ok {
		return false, errors.New("error transaction")
	}

	account := tx.Account
	sequence := libstore.GetSequence(ms, account) + 1
	if tx.Sequence != sequence {
		return false, fmt.Errorf("error sequence: %d != %d", tx.Sequence, sequence)
	}

	info, err := service.GetAccountInfo(account, nil, nil)
	if err != nil || info == nil {
		return false, fmt.Errorf("can't find account %s", account.String())
	}

	feeAmount, err := core.NewAmount(int64(tx.Gas))
	if err != nil {
		return false, err
	}
	remain, err := info.Amount.Subtract(*feeAmount)
	if err != nil {
		return false, err
	}
	if tx.Amount.IsNative() {
		remain, err := remain.Subtract(tx.Amount)
		if err != nil {
			return false, err
		}
		if remain.Less(*feeAmount.ZeroClone()) {
			return false, errors.New("insuffient amount")
		}
	} else {
		info, err = service.GetAccountInfo(account, tx.Amount.Currency, tx.Amount.Issuer)
		if err != nil || info == nil {
			accountKey := core.GetAccountKey(account, tx.Amount.Currency, tx.Amount.Issuer, "/")
			return false, fmt.Errorf("can't find account %s", accountKey)
		}
		remain, err = info.Amount.Subtract(tx.Amount)
		if err != nil {
			return false, err
		}
		if remain.Less(*tx.Amount.ZeroClone()) {
			return false, errors.New("insuffient amount")
		}
	}

	return true, nil
}

func (service *ConsensusService) addBalance(account libcore.Address, amount *core.Amount, sequence uint64) (libblock.State, error) {
	info, _ := service.GetAccountInfo(account, amount.Currency, amount.Issuer)
	if info != nil {
		s, err := block.CloneState(info)
		if err != nil {
			return nil, err
		}
		newAmount, err := info.Amount.Add(*amount)
		if err != nil {
			return nil, err
		}

		info = s.(*block.AccountState)
		info.Amount = *newAmount
		info.Sequence = sequence
	} else {
		info = &block.AccountState{
			State: block.State{
				StateType: block.ACCOUNT_STATE,
				Account:   account,
				Sequence:  sequence,
			},
			Amount: *amount,
		}
	}
	return info, nil
}

func (service *ConsensusService) removeBalance(account libcore.Address, amount *core.Amount, sequence uint64) (libblock.State, error) {
	info, _ := service.GetAccountInfo(account, amount.Currency, amount.Issuer)
	if info != nil {
		s, err := block.CloneState(info)
		if err != nil {
			return nil, err
		}
		newAmount, err := info.Amount.Subtract(*amount)
		if err != nil {
			return nil, err
		}
		if newAmount.Less(*amount.ZeroClone()) {
			return nil, errors.New("insuffient amount")
		}
		info = s.(*block.AccountState)
		info.Amount = *newAmount
		info.Sequence = sequence
		return info, nil
	}
	return nil, errors.New("insuffient amount")
}

func (service *ConsensusService) ProcessTransaction(t libblock.Transaction) (libblock.TransactionWithData, error) {
	config := service.Config
	ms := service.MerkleService

	tx, ok := t.(*block.Transaction)
	if !ok {
		return nil, errors.New("error transaction")
	}

	account := tx.GetAccount()
	sequence := libstore.GetSequence(ms, account) + 1 // sequence of from account, +1
	if tx.Sequence != sequence {
		return nil, fmt.Errorf("error sequence: %d != %d", tx.Sequence, sequence)
	}

	gasAmount, err := core.NewAmount(int64(tx.Gas))
	if err != nil {
		return nil, err
	}

	states := make([]libblock.State, 0)
	if tx.Amount.IsNative() {
		totalAmount, err := tx.Amount.Add(*gasAmount)
		if err != nil {
			return nil, err
		}

		s, err := service.removeBalance(tx.Account, totalAmount, sequence)
		if err != nil {
			return nil, err
		}
		states = append(states, s)
	} else {
		s, err := service.removeBalance(tx.Account, gasAmount, sequence)
		if err != nil {
			return nil, err
		}
		states = append(states, s)

		s, err = service.removeBalance(tx.Account, &tx.Amount, sequence)
		if err != nil {
			return nil, err
		}
		states = append(states, s)
	}

	gasAccount := config.GetGasAccount()
	gasSequence := libstore.GetSequence(ms, gasAccount)
	s, err := service.addBalance(gasAccount, gasAmount, gasSequence) // sequence of gas account, keep unchanged
	if err != nil {
		return nil, err
	}
	states = append(states, s)

	destination := tx.Destination
	destinationSequence := libstore.GetSequence(ms, destination)
	s, err = service.addBalance(destination, &tx.Amount, destinationSequence) // sequence of destination account, keep unchanged
	if err != nil {
		return nil, err
	}
	states = append(states, s)

	r := &block.Receipt{
		TransactionResult: 0,
		States:            states,
	}

	return &block.TransactionWithData{
		Transaction: t,
		Receipt:     r,
		Date:        int64(time.Now().UnixNano() / 1e9),
	}, nil
}
