// Package tronfake is an in-memory TRON full node served over httptest. It is
// TEST SUPPORT ONLY (nothing in production imports it): every TRON test in this
// repo runs against it, NEVER against a real node or mainnet.
//
// It models just enough: blocks with ids/timestamps, a mempool, TRC-20
// transfers with Transfer logs, energy/bandwidth accounting, reorgs and several
// broadcast behaviours (ok, ambiguous, rejected).
package tronfake

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"

	"korzadi-wallet/backend/internal/infrastructure/tronwallet"
)

const (
	blockIntervalMs = 3000
	energyPrice     = 420
	bandwidthPrice  = 1000
	transferTopic   = "ddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"
)

type block struct {
	num uint64
	id  string
	ts  int64
	txs []string
	gen int // bumped on reorg so ids change
}

type account struct {
	balanceSun  int64
	energyLimit int64
	energyUsed  int64
	freeNet     int64               // remaining free bandwidth bytes
	tokens      map[string]*big.Int // contract(b58) -> raw
}

type tx struct {
	id       string
	owner    string
	contract string
	to       string
	amount   *big.Int
	size     int64
	expires  int64
	deposit  bool // synthetic incoming transfer (no signature)
	native   bool // native TRX transfer (amount in sun)
	blockNum uint64
	mined    bool
	info     map[string]any
}

type Chain struct {
	mu      sync.Mutex
	srv     *httptest.Server
	blocks  []*block // blocks[i].num == base+i
	base    uint64
	pending []*tx
	txs     map[string]*tx
	accts   map[string]*account

	energyPerTransfer int64
	pendingVisible    bool
	broadcastQueue    []string // consumed one per broadcast; default "ok"
	rejectCode        string
	broadcasts        int
	failNext          bool // next mined transfer fails with OUT_OF_ENERGY
	gen               int
}

// New starts the fake node with a few blocks already mined.
func New() *Chain {
	c := &Chain{
		txs: map[string]*tx{}, accts: map[string]*account{},
		base: 70_000_000, energyPerTransfer: 31_895, pendingVisible: true,
	}
	c.blocks = append(c.blocks, c.newBlock(c.base, 1_700_000_000_000))
	c.srv = httptest.NewServer(http.HandlerFunc(c.handle))
	c.Mine(30)
	return c
}

func (c *Chain) URL() string { return c.srv.URL }
func (c *Chain) Close()      { c.srv.Close() }

func (c *Chain) newBlock(num uint64, ts int64) *block {
	h := sha256.Sum256([]byte(fmt.Sprintf("blk-%d-%d", num, c.gen)))
	id := make([]byte, 32)
	binary.BigEndian.PutUint64(id[:8], num)
	copy(id[8:], h[:24])
	return &block{num: num, id: hex.EncodeToString(id), ts: ts, gen: c.gen}
}

func (c *Chain) head() *block { return c.blocks[len(c.blocks)-1] }

func (c *Chain) acct(addr string) *account {
	a := c.accts[addr]
	if a == nil {
		a = &account{tokens: map[string]*big.Int{}}
		c.accts[addr] = a
	}
	return a
}

// ---- test controls ----

func (c *Chain) Head() uint64 { c.mu.Lock(); defer c.mu.Unlock(); return c.head().num }

func (c *Chain) SetBalanceSun(addr string, v int64) {
	c.mu.Lock()
	c.acct(addr).balanceSun = v
	c.mu.Unlock()
}
func (c *Chain) SetStaked(addr string, energy, freeNet int64) {
	c.mu.Lock()
	a := c.acct(addr)
	a.energyLimit, a.energyUsed, a.freeNet = energy, 0, freeNet
	c.mu.Unlock()
}
func (c *Chain) SetToken(contract, addr string, raw *big.Int) {
	c.mu.Lock()
	c.acct(addr).tokens[contract] = new(big.Int).Set(raw)
	c.mu.Unlock()
}
func (c *Chain) Token(contract, addr string) *big.Int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if v := c.acct(addr).tokens[contract]; v != nil {
		return new(big.Int).Set(v)
	}
	return new(big.Int)
}
func (c *Chain) SunBalance(addr string) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.acct(addr).balanceSun
}

func (c *Chain) SetEnergyPerTransfer(e int64) { c.mu.Lock(); c.energyPerTransfer = e; c.mu.Unlock() }
func (c *Chain) SetPendingVisible(v bool)     { c.mu.Lock(); c.pendingVisible = v; c.mu.Unlock() }
func (c *Chain) FailNextTransfer()            { c.mu.Lock(); c.failNext = true; c.mu.Unlock() }

// QueueBroadcast programs how the next broadcasts behave:
// "ok" | "ambiguous" (accepted but the HTTP answer is lost) |
// "ambiguous_lost" (NOT accepted and the answer is lost) | "reject" (definitive).
func (c *Chain) QueueBroadcast(modes ...string) {
	c.mu.Lock()
	c.broadcastQueue = append(c.broadcastQueue, modes...)
	c.mu.Unlock()
}
func (c *Chain) SetRejectCode(code string) { c.mu.Lock(); c.rejectCode = code; c.mu.Unlock() }
func (c *Chain) Broadcasts() int           { c.mu.Lock(); defer c.mu.Unlock(); return c.broadcasts }
func (c *Chain) PendingCount() int         { c.mu.Lock(); defer c.mu.Unlock(); return len(c.pending) }

func (c *Chain) TxMined(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := c.txs[id]
	return t != nil && t.mined
}

// Deposit queues an incoming TRC-20 transfer (someone else paying a deposit
// address); it is mined in the next block. Returns its txid.
func (c *Chain) Deposit(contract, from, to string, amount *big.Int) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := fmt.Sprintf("%064x", sha256.Sum256([]byte(fmt.Sprintf("dep-%d-%s-%s-%s", len(c.txs), from, to, amount))))
	t := &tx{id: id, owner: from, contract: contract, to: to, amount: new(big.Int).Set(amount), deposit: true, expires: 1 << 60}
	c.txs[id] = t
	c.pending = append(c.pending, t)
	return id
}

// Mine produces n blocks, including pending transactions in the first one.
func (c *Chain) Mine(n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := 0; i < n; i++ {
		prev := c.head()
		b := c.newBlock(prev.num+1, prev.ts+blockIntervalMs)
		if i == 0 {
			c.includePending(b)
		}
		c.blocks = append(c.blocks, b)
	}
}

func (c *Chain) includePending(b *block) {
	var keep []*tx
	for _, t := range c.pending {
		if t.expires < b.ts { // expired: can NEVER be included
			continue
		}
		c.execute(t, b)
	}
	c.pending = keep
}

// execute applies a transfer to balances/resources and builds its TransactionInfo.
func (c *Chain) execute(t *tx, b *block) {
	t.mined, t.blockNum = true, b.num
	b.txs = append(b.txs, t.id)
	info := map[string]any{"id": t.id, "blockNumber": b.num, "blockTimeStamp": b.ts}
	receipt := map[string]any{"result": "SUCCESS"}
	var fee int64

	if t.native {
		c.executeNative(t, b, info)
		return
	}
	if !t.deposit {
		a := c.acct(t.owner)
		energy := c.energyPerTransfer
		staked := a.energyLimit - a.energyUsed
		if staked < 0 {
			staked = 0
		}
		usedStaked := energy
		if usedStaked > staked {
			usedStaked = staked
		}
		a.energyUsed += usedStaked
		energyFee := (energy - usedStaked) * energyPrice
		var bwFee int64
		if a.freeNet >= t.size {
			a.freeNet -= t.size
		} else {
			bwFee = t.size * bandwidthPrice
		}
		fee = energyFee + bwFee
		receipt["energy_usage_total"] = energy
		receipt["energy_fee"] = energyFee
		receipt["net_fee"] = bwFee

		bal := a.tokens[t.contract]
		failed := c.failNext || a.balanceSun < fee || bal == nil || bal.Cmp(t.amount) < 0
		c.failNext = false
		if failed {
			if fee > a.balanceSun {
				fee = a.balanceSun
			}
			a.balanceSun -= fee
			receipt["result"] = "OUT_OF_ENERGY"
			info["receipt"], info["fee"], info["result"] = receipt, fee, "FAILED"
			t.info = info
			return
		}
		a.balanceSun -= fee
	}

	// token movement
	if t.amount != nil && t.amount.Sign() > 0 {
		if !t.deposit {
			src := c.acct(t.owner).tokens[t.contract]
			c.acct(t.owner).tokens[t.contract] = new(big.Int).Sub(src, t.amount)
		} else if src := c.acct(t.owner).tokens[t.contract]; src != nil && src.Cmp(t.amount) >= 0 {
			c.acct(t.owner).tokens[t.contract] = new(big.Int).Sub(src, t.amount)
		}
		dst := c.acct(t.to).tokens[t.contract]
		if dst == nil {
			dst = new(big.Int)
		}
		c.acct(t.to).tokens[t.contract] = new(big.Int).Add(dst, t.amount)

		amt := make([]byte, 32)
		t.amount.FillBytes(amt)
		info["log"] = []map[string]any{{
			"address": addr20(t.contract),
			"topics":  []string{transferTopic, topicAddr(t.owner), topicAddr(t.to)},
			"data":    hex.EncodeToString(amt),
		}}
	}
	info["receipt"], info["fee"] = receipt, fee
	t.info = info
}

// executeNative applies a native TRX transfer. Like a real node, its
// TransactionInfo carries NO TVM "result" on success.
func (c *Chain) executeNative(t *tx, b *block, info map[string]any) {
	a := c.acct(t.owner)
	var bwFee int64
	if a.freeNet >= t.size {
		a.freeNet -= t.size
	} else {
		bwFee = t.size * bandwidthPrice
	}
	amount := t.amount.Int64()
	if a.balanceSun < amount+bwFee {
		info["fee"], info["result"], info["receipt"] = int64(0), "FAILED", map[string]any{}
		t.info = info
		return
	}
	a.balanceSun -= amount + bwFee
	c.acct(t.to).balanceSun += amount
	info["fee"], info["receipt"] = bwFee, map[string]any{"net_fee": bwFee, "net_usage": t.size}
	t.info = info
}

// Reorg replaces the last `depth` blocks with new ones (new ids). Their
// transactions are DROPPED: the node forgets them (a client may broadcast the
// same bytes again). Undoing balance changes is NOT modelled — tests assert on
// application state (ledger/withdrawal status), not on fake balances after a reorg.
func (c *Chain) Reorg(depth int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if depth <= 0 || depth >= len(c.blocks) {
		return
	}
	c.gen++
	cut := len(c.blocks) - depth
	for _, b := range c.blocks[cut:] {
		for _, id := range b.txs {
			delete(c.txs, id) // dropped from the chain; a node no longer knows it
		}
	}
	rebuilt := make([]*block, 0, depth)
	for _, old := range c.blocks[cut:] {
		rebuilt = append(rebuilt, c.newBlock(old.num, old.ts))
	}
	c.blocks = append(c.blocks[:cut], rebuilt...)
}

// ---- address helpers ----

func addr20(b58 string) string {
	b, err := tronwallet.AddressToBytes21(b58)
	if err != nil {
		return ""
	}
	return hex.EncodeToString(b[1:])
}

func topicAddr(b58 string) string { return strings.Repeat("0", 24) + addr20(b58) }

func fromHex41(h string) string {
	b, err := hex.DecodeString(h)
	if err != nil {
		return ""
	}
	s, _ := tronwallet.AddressFromBytes21(b)
	return s
}

// ---- HTTP ----

func (c *Chain) handle(w http.ResponseWriter, r *http.Request) {
	var req map[string]any
	_ = json.NewDecoder(r.Body).Decode(&req)
	c.mu.Lock()
	defer c.mu.Unlock()

	write := func(v any) { w.Header().Set("Content-Type", "application/json"); _ = json.NewEncoder(w).Encode(v) }
	blockJSON := func(b *block) map[string]any {
		return map[string]any{"blockID": b.id, "block_header": map[string]any{"raw_data": map[string]any{"number": b.num, "timestamp": b.ts}}}
	}
	findBlock := func(num uint64) *block {
		if num < c.base || num > c.head().num {
			return nil
		}
		return c.blocks[num-c.base]
	}
	num := func(key string) (uint64, bool) {
		switch v := req[key].(type) {
		case float64:
			return uint64(v), true
		case string:
			n, err := strconv.ParseUint(v, 10, 64)
			return n, err == nil
		}
		return 0, false
	}

	switch r.URL.Path {
	case "/wallet/getnowblock":
		write(blockJSON(c.head()))
	case "/wallet/getblock", "/wallet/getblockbynum":
		key := "id_or_num"
		if r.URL.Path == "/wallet/getblockbynum" {
			key = "num"
		}
		n, _ := num(key)
		if b := findBlock(n); b != nil {
			write(blockJSON(b))
		} else {
			write(map[string]any{})
		}
	case "/wallet/gettransactioninfobyblocknum":
		n, _ := num("num")
		b := findBlock(n)
		infos := []map[string]any{}
		if b != nil {
			for _, id := range b.txs {
				if t := c.txs[id]; t != nil && t.mined && t.info != nil {
					infos = append(infos, t.info)
				}
			}
		}
		write(infos)
	case "/wallet/gettransactioninfobyid":
		id, _ := req["value"].(string)
		if t := c.txs[id]; t != nil && t.mined && t.info != nil {
			write(t.info)
		} else {
			write(map[string]any{})
		}
	case "/wallet/gettransactionbyid":
		id, _ := req["value"].(string)
		t := c.txs[id]
		if t != nil && (t.mined || c.pendingVisible) {
			write(map[string]any{"txID": id})
		} else {
			write(map[string]any{})
		}
	case "/wallet/getaccount":
		a := fromHex41(fmt.Sprint(req["address"]))
		if acc, ok := c.accts[a]; ok {
			write(map[string]any{"address": req["address"], "balance": acc.balanceSun})
		} else {
			write(map[string]any{})
		}
	case "/wallet/getaccountresource":
		a := c.acct(fromHex41(fmt.Sprint(req["address"])))
		write(map[string]any{"freeNetLimit": 600, "freeNetUsed": 600 - a.freeNet, "EnergyLimit": a.energyLimit, "EnergyUsed": a.energyUsed})
	case "/wallet/getchainparameters":
		write(map[string]any{"chainParameter": []map[string]any{
			{"key": "getEnergyFee", "value": energyPrice}, {"key": "getTransactionFee", "value": bandwidthPrice}}})
	case "/wallet/triggerconstantcontract":
		c.triggerConstant(req, write)
	case "/wallet/broadcasthex":
		c.broadcast(req, w, write)
	default:
		http.NotFound(w, r)
	}
}

func (c *Chain) triggerConstant(req map[string]any, write func(any)) {
	sel, _ := req["function_selector"].(string)
	owner := fromHex41(fmt.Sprint(req["owner_address"]))
	contract := fromHex41(fmt.Sprint(req["contract_address"]))
	param, _ := req["parameter"].(string)
	if (sel == "balanceOf(address)" && len(param) < 64) || (sel == "transfer(address,uint256)" && len(param) < 128) {
		write(map[string]any{"result": map[string]any{"result": false, "message": hex.EncodeToString([]byte("bad parameter"))}})
		return
	}
	ok := map[string]any{"result": true}
	switch sel {
	case "decimals()":
		write(map[string]any{"result": ok, "constant_result": []string{strings.Repeat("0", 63) + "6"}})
	case "balanceOf(address)":
		who := fromHex41("41" + param[24:])
		bal := c.acct(who).tokens[contract]
		if bal == nil {
			bal = new(big.Int)
		}
		write(map[string]any{"result": ok, "constant_result": []string{fmt.Sprintf("%064x", bal)}})
	case "transfer(address,uint256)":
		amt, _ := new(big.Int).SetString(param[64:], 16)
		bal := c.acct(owner).tokens[contract]
		if bal == nil || amt == nil || bal.Cmp(amt) < 0 {
			write(map[string]any{"result": map[string]any{"result": false, "message": hex.EncodeToString([]byte("REVERT opcode executed"))}})
			return
		}
		write(map[string]any{"result": ok, "constant_result": []string{strings.Repeat("0", 63) + "1"}, "energy_used": c.energyPerTransfer})
	default:
		write(map[string]any{"result": map[string]any{"result": false, "message": hex.EncodeToString([]byte("unknown selector"))}})
	}
}

func (c *Chain) broadcast(req map[string]any, w http.ResponseWriter, write func(any)) {
	c.broadcasts++
	mode := "ok"
	if len(c.broadcastQueue) > 0 {
		mode, c.broadcastQueue = c.broadcastQueue[0], c.broadcastQueue[1:]
	}
	raw, _ := hex.DecodeString(fmt.Sprint(req["transaction"]))
	p, err := tronwallet.ParseSignedTx(raw)
	if err != nil || (!p.IsTransfer && !p.IsTRXTransfer) {
		write(map[string]any{"result": false, "code": "CONTRACT_VALIDATE_ERROR", "message": hex.EncodeToString([]byte("bad tx"))})
		return
	}
	reject := func(code, msg string) {
		write(map[string]any{"result": false, "code": code, "txid": p.TxID, "message": hex.EncodeToString([]byte(msg))})
	}
	if signer, serr := p.RecoverSigner(); serr != nil || signer != p.Owner {
		reject("SIGERROR", "validate signature error")
		return
	}
	if p.ExpirationMs <= c.head().ts {
		reject("TRANSACTION_EXPIRATION_ERROR", "transaction expired")
		return
	}
	if t := c.txs[p.TxID]; t != nil {
		reject("DUP_TRANSACTION_ERROR", "dup transaction")
		return
	}
	switch mode {
	case "reject":
		code := c.rejectCode
		if code == "" {
			code = "OTHER_ERROR"
		}
		reject(code, "rejected by test")
		return
	case "ambiguous_lost":
		http.Error(w, "gateway timeout", http.StatusGatewayTimeout)
		return
	}
	t := &tx{id: p.TxID, owner: p.Owner, contract: p.Contract, to: p.To, amount: p.AmountRaw,
		size: int64(len(raw)), expires: p.ExpirationMs}
	if p.IsTRXTransfer {
		t.native, t.amount = true, big.NewInt(p.TRXAmountSun)
	}
	c.txs[t.id] = t
	c.pending = append(c.pending, t)
	if mode == "ambiguous" {
		http.Error(w, "gateway timeout", http.StatusGatewayTimeout)
		return
	}
	write(map[string]any{"result": true, "txid": p.TxID})
}
