package node

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"

	"github.com/tokentransfer/demo/core/pb"

	"github.com/tokentransfer/chain/account"
	"github.com/tokentransfer/chain/block"
	chaincore "github.com/tokentransfer/chain/core"
	"github.com/tokentransfer/chain/crypto"
	"github.com/tokentransfer/chain/node"

	libaccount "github.com/tokentransfer/interfaces/account"
	libblock "github.com/tokentransfer/interfaces/block"
	libcore "github.com/tokentransfer/interfaces/core"
	libcrypto "github.com/tokentransfer/interfaces/crypto"
)

type Status int

const (
	PeerNone Status = iota
	PeerConnected
	PeerKnown
	PeerConsensused
)

func (s Status) String() string {
	switch s {
	case PeerNone:
		return "none"
	case PeerConnected:
		return "connected"
	case PeerKnown:
		return "known"
	case PeerConsensused:
		return "consensused"
	}
	return "unknown status"
}

const (
	BLOCK_DURATION = 10
)

type Peer struct {
	Address string
	Key     libaccount.PublicKey
	Status  Status

	index  uint64
	conn   *grpc.ClientConn
	client pb.NodeServiceClient
}

func (p *Peer) GetPublicKey() libaccount.PublicKey {
	return p.Key
}

func (p *Peer) GetIndex() uint64 {
	if p.index == 0 {
		address, err := p.Key.GenerateAddress()
		if err != nil {
			return 0
		}
		data, err := address.MarshalBinary()
		if err != nil {
			return 0
		}
		n := new(big.Int)
		m := n.SetBytes(data)
		p.index = m.Uint64()
	}
	return p.index
}

type Node struct {
	Consensused bool

	messageId uint64

	cryptoService    *crypto.CryptoService
	merkleService    *node.MerkleService
	consensusService *ConsensusService

	transactions      []libblock.TransactionWithData
	transactionLocker *sync.Mutex

	outs chan *pb.Message
	ins  chan *pb.Message

	ready *sync.WaitGroup
	timer *time.Ticker

	config libcore.Config
	key    libaccount.Key

	self       *Peer
	peers      map[uint64]*Peer
	peerLocker *sync.RWMutex
	bootmap    map[string]*Peer
}

func NewNode() *Node {
	ready := &sync.WaitGroup{}
	ready.Add(1)

	return &Node{
		messageId: uint64(0),

		outs: make(chan *pb.Message, 8),
		ins:  make(chan *pb.Message, 8),

		peers:      map[uint64]*Peer{},
		peerLocker: &sync.RWMutex{},

		bootmap: map[string]*Peer{},
		ready:   ready,

		transactionLocker: &sync.Mutex{},
	}
}

func (n *Node) Init(c libcore.Config) error {
	n.config = c

	key := account.NewKey()
	err := key.UnmarshalText([]byte(c.GetSecret()))
	if err != nil {
		return err
	}
	n.key = key

	pubKey, err := n.key.GetPublic()
	if err != nil {
		return err
	}
	n.self = &Peer{
		Key:     pubKey,
		Address: fmt.Sprintf("%s:%d", c.GetAddress(), c.GetPort()),
		Status:  PeerConsensused,
	}

	return nil
}

func ToInt64(m *map[string]interface{}, key string) int64 {
	s := ToString(m, key)
	if len(s) > 0 {
		n := new(big.Int)
		_, ok := n.SetString(s, 10)
		if ok {
			return n.Int64()
		}
	}
	return 0
}

func ToString(m *map[string]interface{}, key string) string {
	item, ok := (*m)[key]
	if ok {
		s, ok := item.(string)
		if ok {
			return s
		}
	}
	return ""
}

func (n *Node) signTransaction(txm map[string]interface{}) (string, *block.Transaction, error) {
	from := ToString(&txm, "from")
	secret := ToString(&txm, "secret")
	to := ToString(&txm, "to")
	value := ToInt64(&txm, "value")

	fromKey := account.NewKey()
	err := fromKey.UnmarshalText([]byte(secret))
	if err != nil {
		return "", nil, err
	}
	fromAccount, err := fromKey.GetAddress()
	if err != nil {
		return "", nil, err
	}
	fromAddress, err := fromAccount.GetAddress()
	if err != nil {
		return "", nil, err
	}
	if fromAddress != from {
		return "", nil, fmt.Errorf("error account: %s != %s", fromAddress, from)
	}
	toAccount := account.NewAddress()
	err = toAccount.UnmarshalText([]byte(to))
	if err != nil {
		return "", nil, err
	}
	seq := n.getNextSequence(fromAddress)
	tx := &block.Transaction{
		TransactionType: libblock.TransactionType(1),

		Account:     fromAccount,
		Sequence:    seq,
		Amount:      value,
		Gas:         int64(10),
		Destination: toAccount,
	}
	err = n.cryptoService.Sign(fromKey, tx)
	if err != nil {
		return "", nil, err
	}
	data, err := tx.MarshalBinary()
	if err != nil {
		return "", nil, err
	}
	blob := libcore.Bytes(data).String()
	return blob, tx, nil
}

func (n *Node) sendTransaction(txm map[string]interface{}) (libblock.TransactionWithData, libblock.Transaction, error) {
	_, tx, err := n.signTransaction(txm)
	if err != nil {
		return nil, nil, err
	}
	txWithData, _, err := n.processTransaction(tx)
	if err != nil {
		return nil, nil, err
	}
	return txWithData, tx, nil
}

func (n *Node) processTransaction(tx *block.Transaction) (libblock.TransactionWithData, libblock.Transaction, error) {
	n.transactionLocker.Lock()
	defer n.transactionLocker.Unlock()

	h, _, err := n.cryptoService.Raw(tx, libcrypto.RawBinary)
	if err != nil {
		return nil, nil, err
	}
	ok, err := n.consensusService.VerifyTransaction(tx)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, errors.New("error transaction")
	}
	txWithData, err := n.consensusService.ProcessTransaction(tx)
	if err != nil {
		return nil, nil, err
	}

	// util.PrintJSON("txWithData", txWithData)
	_, ok = n.AddTransaction(txWithData)
	if ok {
		fmt.Println(">>> receive transaction", h.String())
	} else {
		fmt.Println(">>> drop transaction", h.String())
	}

	return txWithData, tx, nil
}

func (n *Node) getNextSequence(address string) uint64 {
	accountEntry, err := n.consensusService.GetAccount(address)
	if err != nil {
		return uint64(1)
	}
	return accountEntry.Sequence + 1
}

func (n *Node) Call(method string, params []interface{}) (interface{}, error) {
	switch method {
	case "blockNumber":
		result := n.consensusService.GetBlockNumber()
		return result, nil
	case "getBalance":
		address := params[0].(string)
		accountEntry, err := n.consensusService.GetAccount(address)
		if err != nil {
			return nil, err
		}
		return accountEntry.Amount, nil
	case "getTransactionCount":
		address := params[0].(string)
		seq := n.getNextSequence(address)
		return seq, nil
	case "getTransactionReceipt":
		hashString := params[0].(string)
		h, err := hex.DecodeString(hashString)
		if err != nil {
			return nil, err
		}
		txWithData, err := n.merkleService.GetTransactionByHash(libcore.Hash(h))
		if err != nil {
			return nil, err
		}
		receipt := txWithData.GetReceipt()
		_, err = n.HashReceipt(receipt)
		if err != nil {
			return nil, err
		}
		return receipt, nil
	case "getTransactionByHash":
		hashString := params[0].(string)
		h, err := hex.DecodeString(hashString)
		if err != nil {
			return nil, err
		}
		txWithData, err := n.merkleService.GetTransactionByHash(libcore.Hash(h))
		if err != nil {
			return nil, err
		}
		_, err = n.HashTransaction(txWithData)
		if err != nil {
			return nil, err
		}
		return txWithData, nil
	case "getTransactionByIndex":
		address := params[0].(string)
		a := account.NewAddress()
		err := a.UnmarshalText([]byte(address))
		if err != nil {
			return nil, err
		}
		index := uint64(params[1].(float64))

		txWithData, err := n.merkleService.GetTransactionByIndex(a, index)
		if err != nil {
			return nil, err
		}
		_, err = n.HashTransaction(txWithData)
		if err != nil {
			return nil, err
		}
		return txWithData, nil
	case "getBlockByHash":
		hashString := params[0].(string)
		h, err := hex.DecodeString(hashString)
		if err != nil {
			return nil, err
		}
		block, err := n.merkleService.GetBlockByHash(libcore.Hash(h))
		if err != nil {
			return nil, err
		}
		_, err = n.HashBlock(block)
		if err != nil {
			return nil, err
		}
		return block, nil
	case "getBlockByNumber":
		index := uint64(params[0].(float64))
		block, err := n.merkleService.GetBlockByIndex(index)
		if err != nil {
			return nil, err
		}
		_, err = n.HashBlock(block)
		if err != nil {
			return nil, err
		}
		return block, nil
	case "signTransaction":
		l := len(params)
		list := make([]string, l)
		for i := 0; i < l; i++ {
			item := params[i].(map[string]interface{})
			blob, tx, err := n.signTransaction(item)
			if err != nil {
				return nil, err
			}
			hash := tx.GetHash()
			list[i] = blob
			fmt.Println("sign transaction", hash.String(), blob)
		}
		return list, nil
	case "sendTransaction":
		l := len(params)
		list := make([]string, l)
		for i := 0; i < l; i++ {
			item := params[i].(map[string]interface{})
			_, tx, err := n.sendTransaction(item)
			if err != nil {
				return nil, err
			}

			data, err := tx.MarshalBinary()
			if err != nil {
				return nil, err
			}
			n.broadcast(data)

			hash := tx.GetHash()
			list[i] = hash.String()
			fmt.Println("send transaction", hash.String())
		}
		return list, nil
	case "sendRawTransaction":
		l := len(params)
		list := make([]string, l)
		for i := 0; i < l; i++ {
			blob := params[i].(string)

			data, err := hex.DecodeString(blob)
			if err != nil {
				return nil, err
			}
			tx := &block.Transaction{}
			err = tx.UnmarshalBinary(data)
			if err != nil {
				return nil, err
			}
			_, _, err = n.processTransaction(tx)
			if err != nil {
				return nil, err
			}
			n.broadcast(data)

			hash := tx.GetHash()
			list[i] = hash.String()
			fmt.Println("send raw transaction", hash.String())
		}
		return list, nil
	default:
		return nil, fmt.Errorf("no such method %s", method)
	}
}

func (n *Node) StartServer() {
	host := n.config.GetAddress()
	port := n.config.GetPort()
	listen, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		panic(err)
	}

	account, err := n.self.GetPublicKey().GenerateAddress()
	if err != nil {
		panic(err)
	}
	address, err := account.GetAddress()
	if err != nil {
		panic(err)
	}

	grpcServer := grpc.NewServer()
	pb.RegisterNodeServiceServer(grpcServer, n)
	fmt.Println("Listening on", fmt.Sprintf("%s:%d", host, port))
	fmt.Println("peer", address, n.self.GetIndex())
	err = grpcServer.Serve(listen)
	if err != nil {
		panic(err)
	}
}

func (n *Node) IsCurrent() bool {
	blockNumber := (n.consensusService.GetBlockNumber() + 1)
	list := n.config.GetBootstraps()
	l := len(list)
	s := list[int(blockNumber%int64(l))]
	if s == n.self.Address {
		return true
	}
	return false
}

func (n *Node) AddTransaction(txWithData libblock.TransactionWithData) (libcore.Hash, bool) {
	h, err := n.HashTransaction(txWithData)
	if err != nil {
		return nil, false
	}

	for i := 0; i < len(n.transactions); i++ {
		txWithData := n.transactions[i]
		hi := txWithData.GetTransaction().GetHash()
		if bytes.Equal(h, hi) {
			return nil, false
		}
	}

	n.transactions = append(n.transactions, txWithData)
	return h, true
}

func (n *Node) ClearTransaction(b libblock.Block) {
	n.transactionLocker.Lock()
	defer n.transactionLocker.Unlock()

	m := make(map[string]bool)
	list := b.GetTransactions()
	for i := 0; i < len(list); i++ {
		txWithData := list[i]
		h := txWithData.GetTransaction().GetHash()
		m[h.String()] = true
	}

	transactions := make([]libblock.TransactionWithData, 0)
	for i := 0; i < len(n.transactions); i++ {
		txWithData := n.transactions[i]
		h := txWithData.GetTransaction().GetHash()
		exists, ok := m[h.String()]
		if ok && exists {
			fmt.Println(">>> drop block transaction", h.String())
		} else {
			transactions = append(transactions, txWithData)
		}
	}
	n.transactions = transactions
}

func (n *Node) GenerateBlock() (libblock.Block, error) {
	n.transactionLocker.Lock()
	defer n.transactionLocker.Unlock()

	list := n.transactions
	n.transactions = make([]libblock.TransactionWithData, 0)

	return n.consensusService.GenerateBlock(list)
}

func (n *Node) generate() {
	n.ready.Wait()

	now := time.Now()
	nowTime := now.UnixNano()
	nextTime := (nowTime - nowTime%int64(BLOCK_DURATION*time.Second)) + int64(BLOCK_DURATION*time.Second)
	duration := (nextTime - nowTime)
	if duration > 0 {
		time.Sleep(time.Duration(nextTime - nowTime))
	}

	n.timer = time.NewTicker(BLOCK_DURATION * time.Second)
	for {
		t := <-n.timer.C

		if n.IsCurrent() {
			block, err := n.GenerateBlock()
			if err != nil {
				panic(err)
			}
			_, err = n.HashBlock(block)
			if err != nil {
				panic(err)
			}
			fmt.Printf("=== generate block %d, %s, %d\n", block.GetIndex(), t.String(), len(block.GetTransactions()))

			_, err = n.consensusService.VerifyBlock(block)
			if err != nil {
				panic(err)
			}

			err = n.consensusService.AddBlock(block)
			if err != nil {
				panic(err)
			}

			data, err := block.MarshalBinary()
			if err != nil {
				panic(err)
			}
			n.broadcast(data)
		}
	}
}

func (n *Node) newId() uint64 {
	return atomic.AddUint64(&n.messageId, 1)
}

func (n *Node) broadcast(data []byte) {
	msg := &pb.Message{
		Id:        n.newId(),
		Data:      data,
		NodeIndex: n.self.GetIndex(),
	}
	n.outs <- msg
}

func (n *Node) connect() {
	lastConsensused := n.Consensused
	for {
		list := n.ListPeer()

		fmt.Println(">>>", 0, n.self.GetIndex(), n.self.Address, n.self.Status)
		for i := 0; i < len(list); i++ {
			p := list[i]
			fmt.Println(">>>", i+1, p.GetIndex(), p.Address, p.Status)
		}

		if !lastConsensused && n.Consensused {
			lastConsensused = n.Consensused
			n.ready.Done()
		}

		time.Sleep(10 * time.Second)
	}
}

func (n *Node) ConnectTo(p *Peer) {
	fmt.Println("dial", p.Address)
	conn, err := grpc.Dial(p.Address, grpc.WithInsecure(), grpc.WithReturnConnectionError(), grpc.WithTimeout(10*time.Second))
	if err != nil {
		log.Println(err)
	} else {
		p.conn = conn
		p.client = pb.NewNodeServiceClient(conn)
		p.Status = PeerConnected
		fmt.Println("connected to", p.Address)
	}
}

func (n *Node) SendRequestInfo(p *Peer) {
	fmt.Println("request to", p.Address)
	reply, err := p.client.SendRequest(context.Background(), &pb.RequestInfo{
		Address: n.self.Address,
	})
	if err != nil {
		log.Println(err)
	} else {
		fmt.Println("reply from", p.Address)

		data := reply.GetPublicKey()
		publicKey := account.NewPublicKey()
		err := publicKey.UnmarshalBinary(data)
		if err != nil {
			log.Println(err)
		} else {
			address := reply.GetAddress()
			p := n.bootmap[address]
			if p != nil {
				p.Key = publicKey
				p.Status = PeerKnown
				n.AddPeer(p)
			}
		}
	}
}

func (n *Node) PrepareConsensus() bool {
	list := n.ListPeer()
	count := 0
	for i := 0; i < len(list); i++ {
		p := list[i]
		if p.Status >= PeerKnown {
			count++
		}
	}
	if count == len(n.bootmap) {
		for i := 0; i < len(list); i++ {
			p := list[i]
			p.Status = PeerConsensused
		}
		return true
	}
	return false
}

func (n *Node) discoveryPeer(p *Peer) {
	for {
		switch p.Status {
		case PeerNone:
			n.ConnectTo(p)
		case PeerConnected:
			n.SendRequestInfo(p)
		case PeerKnown:
			n.Consensused = n.PrepareConsensus()
		}

		time.Sleep(10 * time.Second)
	}
}

func (n *Node) discovery() {
	bootstraps := n.config.GetBootstraps()
	for i := 0; i < len(bootstraps); i++ {
		address := bootstraps[i]
		if address != n.self.Address {
			p := n.bootmap[address]
			if p == nil {
				p = &Peer{
					Address: address,
					Status:  PeerNone,
				}
				n.bootmap[address] = p

				go n.discoveryPeer(p)
			}
		}
	}
}

func (n *Node) load() {
	cryptoService := &crypto.CryptoService{}
	merkleService := &node.MerkleService{
		CryptoService: cryptoService,
	}
	err := merkleService.Init(n.config)
	if err != nil {
		panic(err)
	}
	err = merkleService.Start()
	if err != nil {
		panic(err)
	}
	consensusService := &ConsensusService{
		CryptoService: cryptoService,
		MerkleService: merkleService,
		Config:        n.config,
	}

	n.cryptoService = cryptoService
	n.merkleService = merkleService
	n.consensusService = consensusService
}

func (n *Node) send() {
	for {
		m := <-n.outs
		data := m.GetData()
		list := n.ListPeer()
		for i := 0; i < len(list); i++ {
			p := list[i]
			_, err := p.client.SendMessage(context.Background(), m)
			if err != nil {
				fmt.Println(">>>", chaincore.GetInfo(data), libcore.Bytes(data).String())
				log.Println(err)
			} else {
				fmt.Printf(">>> send %d(%s) to %d\n", m.Id, chaincore.GetInfo(data), p.GetIndex())
			}
		}
	}
}

func (n *Node) receive() {
	for {
		m := <-n.ins
		data := m.GetData()
		fromIndex := m.GetNodeIndex()
		fromPeer, ok := n.peers[fromIndex]
		if ok {
			fmt.Printf("<<< receive message %d(%s) from %d\n", m.Id, chaincore.GetInfo(data), fromPeer.GetIndex())
			if len(data) > 0 {
				meta := data[0]
				switch meta {
				case chaincore.CORE_BLOCK:
					b := &block.Block{}
					err := b.UnmarshalBinary(data)
					if err != nil {
						log.Println(err)
						continue
					}
					h, err := n.HashBlock(b)
					if err != nil {
						log.Println(err)
						continue
					}
					fmt.Println("<<< receive block", b.GetIndex(), h.String(), len(b.GetTransactions()))
					_, err = n.consensusService.VerifyBlock(b)
					if err != nil {
						log.Println(err)
						continue
					}
					err = n.consensusService.AddBlock(b)
					if err != nil {
						log.Println(err)
						continue
					}
					n.ClearTransaction(b)
				case chaincore.CORE_TRANSACTION:
					tx := &block.Transaction{}
					err := tx.UnmarshalBinary(data)
					if err != nil {
						log.Println(err)
						continue
					}
					_, _, err = n.processTransaction(tx)
					if err != nil {
						log.Println(err)
						continue
					}
				default:
					fmt.Println("<<< error message")
				}
			} else {
				fmt.Println("<<< null message")
			}
		} else {
			fmt.Println("<<< unknown peer", fromIndex)
		}
	}
}

func (n *Node) Start() error {
	n.load()

	go n.send()
	go n.receive()

	go n.connect()
	go n.discovery()
	go n.generate()

	return nil
}

func (n *Node) GetNodeKey() libaccount.Key {
	return n.key
}

func (n *Node) AddPeer(p *Peer) error {
	n.peerLocker.Lock()
	defer n.peerLocker.Unlock()

	n.peers[p.GetIndex()] = p
	return nil
}

func (n *Node) RemovePeer(p *Peer) error {
	n.peerLocker.Lock()
	defer n.peerLocker.Unlock()

	delete(n.peers, p.GetIndex())
	return nil
}

func (n *Node) ListPeer() []*Peer {
	n.peerLocker.RLock()
	defer n.peerLocker.RUnlock()

	list := make([]*Peer, 0)
	for _, p := range n.peers {
		list = append(list, p)
	}
	return list
}

func (n *Node) HashBlock(b libblock.Block) (libcore.Hash, error) {
	h, _, err := n.cryptoService.Raw(b, libcrypto.RawBinary)
	if err != nil {
		return nil, err
	}
	transactions := b.GetTransactions()
	for i := 0; i < len(transactions); i++ {
		txWithData := transactions[i]
		_, err := n.HashTransaction(txWithData)
		if err != nil {
			return nil, err
		}
	}
	states := b.GetStates()
	for i := 0; i < len(states); i++ {
		state := states[i]
		_, err := n.HashState(state)
		if err != nil {
			return nil, err
		}
	}
	return h, nil
}

func (n *Node) HashTransaction(txWithData libblock.TransactionWithData) (libcore.Hash, error) {
	_, _, err := n.cryptoService.Raw(txWithData, libcrypto.RawBinary)
	if err != nil {
		return nil, err
	}

	h, _, err := n.cryptoService.Raw(txWithData.GetTransaction(), libcrypto.RawBinary)
	if err != nil {
		return nil, err
	}

	_, _, err = n.cryptoService.Raw(txWithData.GetReceipt(), libcrypto.RawBinary)
	if err != nil {
		return nil, err
	}

	_, err = n.HashReceipt(txWithData.GetReceipt())
	if err != nil {
		return nil, err
	}
	return h, nil
}

func (n *Node) HashReceipt(r libblock.Receipt) (libcore.Hash, error) {
	h, _, err := n.cryptoService.Raw(r, libcrypto.RawBinary)
	if err != nil {
		return nil, err
	}

	states := r.GetStates()
	for i := 0; i < len(states); i++ {
		state := states[i]
		_, err := n.HashState(state)
		if err != nil {
			return nil, err
		}
	}
	return h, nil
}

func (n *Node) HashState(s libblock.State) (libcore.Hash, error) {
	h, _, err := n.cryptoService.Raw(s, libcrypto.RawBinary)
	if err != nil {
		return nil, err
	}
	return h, nil
}

func (n *Node) SendMessage(c context.Context, m *pb.Message) (*pb.Reply, error) {
	n.ins <- m
	return &pb.Reply{}, nil
}

func (n *Node) SendRequest(c context.Context, req *pb.RequestInfo) (*pb.ReplyInfo, error) {
	fmt.Println("<<< receive request from", req.GetAddress())

	data, err := n.self.Key.MarshalBinary()
	if err != nil {
		log.Println(err)
		return nil, err
	}
	return &pb.ReplyInfo{
		PublicKey: data,
		Address:   n.self.Address,
	}, nil
}
