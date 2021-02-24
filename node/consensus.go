package node

import (
	"errors"
	"fmt"
	"time"

	"github.com/tokentransfer/chain/account"
	"github.com/tokentransfer/chain/block"

	libblock "github.com/tokentransfer/interfaces/block"
	libcrypto "github.com/tokentransfer/interfaces/crypto"
	libnode "github.com/tokentransfer/interfaces/node"
)

type ConsensusService struct {
	CryptoService libcrypto.CryptoService
	MerkleService libnode.MerkleService

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
	cs := service.CryptoService

	var b *block.Block
	if service.ValidatedBlock == nil { //genesis
		if len(list) > 0 {
			return nil, errors.New("error genesis block")
		}

		rootKey, err := account.GenerateFamilySeed("masterpassphrase")
		if err != nil {
			return nil, err
		}
		rootAccount, err := rootKey.GetAddress()
		if err != nil {
			return nil, err
		}
		receipts := []libblock.Receipt{
			&block.Receipt{
				TransactionIndex:  uint32(0),
				TransactionResult: libblock.TransactionResult(0),
				States: []libblock.State{
					&block.CurrencyState{
						State: block.State{
							BlockIndex: uint64(0),
						},
						Account:     rootAccount,
						Sequence:    uint64(0),
						Name:        "TEST Coin",
						Symbol:      "TEST",
						Decimals:    6,
						TotalSupply: int64(100000000000000),
					},
					&block.AccountState{
						State: block.State{
							BlockIndex: uint64(0),
						},
						Account:  rootAccount,
						Sequence: uint64(0),
						Amount:   int64(100000000000000),
					},
				},
			},
		}

		ms := service.MerkleService
		for i := 0; i < len(receipts); i++ {
			r := receipts[i]
			err := ms.PutReceipt(r)
			if err != nil {
				return nil, err
			}
		}

		b = &block.Block{
			BlockIndex: uint64(0),
			ParentHash: libcrypto.ZeroHash(cs),

			Transactions:    []libblock.TransactionWithData{},
			TransactionHash: ms.GetTransactionRoot(),

			Receipts:    receipts,
			ReceiptHash: ms.GetReceiptRoot(),

			Timestamp: time.Now().UnixNano(),
		}

		err = ms.Cancel()
		if err != nil {
			return nil, err
		}
	} else {
		ms := service.MerkleService
		v := service.ValidatedBlock

		receipts := make([]libblock.Receipt, 0)
		fmt.Printf("=== package %d transactions in block %d\n", len(list), v.GetIndex()+1)
		for i := 0; i < len(list); i++ {
			tx := list[i]
			receipts = append(receipts, tx.GetReceipt())

			fmt.Printf("=== %d %s\n", i, tx.GetTransaction().GetHash().String())
		}

		for i := 0; i < len(list); i++ {
			txWithData := list[i]

			r := txWithData.GetReceipt()
			r.SetTransactionIndex(uint32(i))
			states := r.GetStates()
			for j := 0; j < len(states); j++ {
				s := states[j]
				s.SetBlockIndex(v.GetIndex() + 1)
			}

			err := ms.PutTransaction(txWithData)
			if err != nil {
				return nil, err
			}
		}

		b = &block.Block{
			BlockIndex: v.GetIndex() + 1,
			ParentHash: v.GetHash(),

			Transactions:    list,
			TransactionHash: ms.GetTransactionRoot(),

			Receipts:    receipts,
			ReceiptHash: ms.GetReceiptRoot(),

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

		ok, err = ms.VerifyTransaction(tx)
		if err != nil {
			return
		}
		if !ok {
			err = errors.New("verify transaction failed")
			return
		}

		newWithData, e := ms.ProcessTransaction(tx)
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
	if b.GetIndex() == 0 { // genesis block
		receipts := b.GetReceipts()
		l := len(receipts)
		for i := 0; i < l; i++ {
			r := receipts[i]
			err = ms.PutReceipt(r)
			if err != nil {
				ok = false
				return
			}
		}
	}

	transactionHash := ms.GetTransactionRoot()
	receiptHash := ms.GetReceiptRoot()
	if !b.GetTransactionHash().Equals(transactionHash) {
		ok = false
		err = fmt.Errorf("error transaction hash: %s != %s", b.GetTransactionHash().String(), transactionHash.String())
		return
	}
	if !b.GetReceiptHash().Equals(receiptHash) {
		ok = false
		err = fmt.Errorf("error receipt hash: %s != %s", b.GetReceiptHash().String(), receiptHash.String())
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
