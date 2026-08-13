// Package evm is the EVM half of the facilitator's chain access: it verifies
// EIP-712 signatures, reads chain state and broadcasts settlement transactions
// for one network.
//
// The Signer here satisfies two consumers — the x402 mechanisms, which call it
// through the SDK's FacilitatorEvmSigner interface, and the wallet use cases,
// which read its balance through domain/wallet.BalanceReader.
package evm

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/math"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
	evmmech "github.com/x402-foundation/x402/go/v2/mechanisms/evm"

	"github.com/islu/x402-facilitator/internal/domain/wallet"
)

// weiPerEther converts the chain's atomic unit to whole ETH.
const weiPerEther = 1e18

// ErrGasPriceTooHigh is returned instead of broadcasting when the network's
// suggested fee exceeds the configured ceiling. Settlement fails loudly rather
// than silently overpaying.
var ErrGasPriceTooHigh = errors.New("gas price above configured maximum")

// Config configures an EVM facilitator signer.
type Config struct {
	// PrivateKey is the settlement wallet key, hex encoded, 0x prefix optional.
	PrivateKey string
	RPCURL     string
	// ExpectedChainID is checked against the RPC endpoint at construction so a
	// wrong URL cannot make the facilitator settle on another chain.
	ExpectedChainID *big.Int
	// MaxFeePerGasWei caps the fee per gas the signer will pay. Nil is uncapped.
	MaxFeePerGasWei *big.Int
	// GasLimit is used when gas estimation fails.
	GasLimit uint64
	// ReceiptTimeout bounds WaitForTransactionReceipt.
	ReceiptTimeout time.Duration
	// ReceiptPollInterval controls receipt polling frequency.
	ReceiptPollInterval time.Duration
}

func (c *Config) applyDefaults() {
	if c.GasLimit == 0 {
		c.GasLimit = 300_000
	}
	if c.ReceiptTimeout == 0 {
		c.ReceiptTimeout = 60 * time.Second
	}
	if c.ReceiptPollInterval == 0 {
		c.ReceiptPollInterval = time.Second
	}
}

// Signer implements evmmech.FacilitatorEvmSigner against a single EVM network.
//
// It owns one settlement wallet and serializes nonce assignment so concurrent
// settlements cannot collide on the same nonce.
type Signer struct {
	cfg     Config
	key     *ecdsa.PrivateKey
	address common.Address
	client  *ethclient.Client
	chainID *big.Int
	network string

	nonceMu    sync.Mutex
	nextNonce  uint64
	nonceValid bool
}

// compile-time check that we satisfy the interface the mechanisms call.
var _ evmmech.FacilitatorEvmSigner = (*Signer)(nil)

// New dials the RPC endpoint and prepares a settlement wallet for network.
//
// It fails if the key is unusable, the endpoint is unreachable, or the chain ID
// the endpoint reports differs from the one encoded in the CAIP-2 network id.
func New(ctx context.Context, network string, cfg Config) (*Signer, error) {
	cfg.applyDefaults()

	key, err := crypto.HexToECDSA(strings.TrimPrefix(strings.TrimSpace(cfg.PrivateKey), "0x"))
	if err != nil {
		return nil, fmt.Errorf("parse private key for %s: %w", network, err)
	}

	client, err := ethclient.DialContext(ctx, cfg.RPCURL)
	if err != nil {
		return nil, fmt.Errorf("dial RPC for %s: %w", network, err)
	}

	chainID, err := client.ChainID(ctx)
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("read chain ID for %s: %w", network, err)
	}
	if cfg.ExpectedChainID != nil && chainID.Cmp(cfg.ExpectedChainID) != 0 {
		client.Close()
		return nil, fmt.Errorf("RPC for %s reports chain ID %s, expected %s", network, chainID, cfg.ExpectedChainID)
	}

	return &Signer{
		cfg:     cfg,
		key:     key,
		address: crypto.PubkeyToAddress(key.PublicKey),
		client:  client,
		chainID: chainID,
		network: network,
	}, nil
}

// Network returns the CAIP-2 network this signer settles on.
func (s *Signer) Network() string { return s.network }

// Address returns the settlement wallet address.
func (s *Signer) Address() string { return s.address.Hex() }

// Close releases the RPC connection.
func (s *Signer) Close() { s.client.Close() }

// GetAddresses returns the settlement wallet addresses.
func (s *Signer) GetAddresses() []string { return []string{s.address.Hex()} }

// GetChainID returns the chain ID reported by the RPC endpoint at startup.
func (s *Signer) GetChainID(context.Context) (*big.Int, error) { return s.chainID, nil }

// VerifyTypedData recovers the signer of an EIP-712 signature and compares it
// with address. Used for EOA payment authorizations; smart-wallet (ERC-1271 /
// ERC-6492) signatures are verified on-chain by the mechanism itself.
func (s *Signer) VerifyTypedData(
	_ context.Context,
	address string,
	domain evmmech.TypedDataDomain,
	types map[string][]evmmech.TypedDataField,
	primaryType string,
	message map[string]any,
	signature []byte,
) (bool, error) {
	if len(signature) != 65 {
		return false, fmt.Errorf("invalid signature length: %d", len(signature))
	}

	typedData := apitypes.TypedData{
		Types:       apitypes.Types{},
		PrimaryType: primaryType,
		Domain: apitypes.TypedDataDomain{
			Name:              domain.Name,
			Version:           domain.Version,
			ChainId:           (*math.HexOrDecimal256)(domain.ChainID),
			VerifyingContract: domain.VerifyingContract,
		},
		Message: message,
	}
	for name, fields := range types {
		converted := make([]apitypes.Type, len(fields))
		for i, field := range fields {
			converted[i] = apitypes.Type{Name: field.Name, Type: field.Type}
		}
		typedData.Types[name] = converted
	}

	// Declare only the domain fields that are actually populated.
	// apitypes.TypedDataDomain.Map() drops empty values; naming a dropped field
	// in the type list makes HashStruct fail (Permit2's version-less domain is
	// the canonical case). This mirrors viem's getTypesForEIP712Domain.
	if _, ok := typedData.Types["EIP712Domain"]; !ok {
		fields := make([]apitypes.Type, 0, 4)
		if typedData.Domain.Name != "" {
			fields = append(fields, apitypes.Type{Name: "name", Type: "string"})
		}
		if typedData.Domain.Version != "" {
			fields = append(fields, apitypes.Type{Name: "version", Type: "string"})
		}
		if typedData.Domain.ChainId != nil {
			fields = append(fields, apitypes.Type{Name: "chainId", Type: "uint256"})
		}
		if typedData.Domain.VerifyingContract != "" {
			fields = append(fields, apitypes.Type{Name: "verifyingContract", Type: "address"})
		}
		typedData.Types["EIP712Domain"] = fields
	}

	structHash, err := typedData.HashStruct(typedData.PrimaryType, typedData.Message)
	if err != nil {
		return false, fmt.Errorf("hash message: %w", err)
	}
	domainSeparator, err := typedData.HashStruct("EIP712Domain", typedData.Domain.Map())
	if err != nil {
		return false, fmt.Errorf("hash domain: %w", err)
	}

	preimage := append([]byte{0x19, 0x01}, domainSeparator...)
	preimage = append(preimage, structHash...)
	digest := crypto.Keccak256(preimage)

	// go-ethereum expects the recovery id in [0,1]; wallets send [27,28].
	normalized := make([]byte, 65)
	copy(normalized, signature)
	if normalized[64] >= 27 {
		normalized[64] -= 27
	}

	pubKey, err := crypto.SigToPub(digest, normalized)
	if err != nil {
		return false, fmt.Errorf("recover signer: %w", err)
	}

	recovered := crypto.PubkeyToAddress(*pubKey)
	return bytes.Equal(recovered.Bytes(), common.HexToAddress(address).Bytes()), nil
}

// ReadContract performs an eth_call and returns the first output value.
//
// The call is made from the facilitator address: settlement proxies such as
// the upto and exact schemes enforce msg.sender == witness.facilitator, so a
// zero From would make their simulation reads revert.
func (s *Signer) ReadContract(
	ctx context.Context,
	contractAddress string,
	abiJSON []byte,
	method string,
	args ...any,
) (any, error) {
	contractABI, err := abi.JSON(bytes.NewReader(abiJSON))
	if err != nil {
		return nil, fmt.Errorf("parse ABI: %w", err)
	}
	methodABI, ok := contractABI.Methods[method]
	if !ok {
		return nil, fmt.Errorf("method %s not found in ABI", method)
	}

	data, err := contractABI.Pack(method, args...)
	if err != nil {
		return nil, fmt.Errorf("pack %s: %w", method, err)
	}

	to := common.HexToAddress(contractAddress)
	result, err := s.client.CallContract(ctx, ethereum.CallMsg{From: s.address, To: &to, Data: data}, nil)
	if err != nil {
		return nil, fmt.Errorf("call %s: %w", method, err)
	}

	if len(methodABI.Outputs) == 0 {
		return nil, nil
	}
	outputs, err := methodABI.Outputs.Unpack(result)
	if err != nil {
		return nil, fmt.Errorf("unpack %s: %w", method, err)
	}
	if len(outputs) == 0 {
		return nil, nil
	}
	return outputs[0], nil
}

// WriteContract encodes and broadcasts a contract call, appending dataSuffix
// (the builder-code attribution bytes) to the calldata.
func (s *Signer) WriteContract(
	ctx context.Context,
	contractAddress string,
	abiJSON []byte,
	method string,
	dataSuffix []byte,
	args ...any,
) (string, error) {
	contractABI, err := abi.JSON(bytes.NewReader(abiJSON))
	if err != nil {
		return "", fmt.Errorf("parse ABI: %w", err)
	}
	data, err := contractABI.Pack(method, args...)
	if err != nil {
		return "", fmt.Errorf("pack %s: %w", method, err)
	}
	return s.SendTransaction(ctx, contractAddress, evmmech.AppendDataSuffix(data, dataSuffix))
}

// SendTransaction signs and broadcasts a transaction with pre-encoded calldata.
func (s *Signer) SendTransaction(ctx context.Context, to string, data []byte) (string, error) {
	toAddress := common.HexToAddress(to)

	tip, feeCap, err := s.feesPerGas(ctx)
	if err != nil {
		return "", err
	}
	gasLimit := s.estimateGas(ctx, toAddress, data)

	nonce, err := s.reserveNonce(ctx)
	if err != nil {
		return "", err
	}

	tx := gethtypes.NewTx(&gethtypes.DynamicFeeTx{
		ChainID:   s.chainID,
		Nonce:     nonce,
		To:        &toAddress,
		Value:     big.NewInt(0),
		Gas:       gasLimit,
		GasTipCap: tip,
		GasFeeCap: feeCap,
		Data:      data,
	})

	signed, err := gethtypes.SignTx(tx, gethtypes.LatestSignerForChainID(s.chainID), s.key)
	if err != nil {
		s.invalidateNonce()
		return "", fmt.Errorf("sign transaction: %w", err)
	}

	if err := s.client.SendTransaction(ctx, signed); err != nil {
		// The reserved nonce was never consumed on-chain (or was consumed by
		// someone else); resync from the node on the next send.
		s.invalidateNonce()
		return "", fmt.Errorf("broadcast transaction: %w", err)
	}

	return signed.Hash().Hex(), nil
}

// WaitForTransactionReceipt polls until the transaction is mined, the deadline
// passes or ctx is cancelled. Logs are included so the mechanism can confirm
// the ERC-20 Transfer event actually matches the payment.
func (s *Signer) WaitForTransactionReceipt(ctx context.Context, txHash string) (*evmmech.TransactionReceipt, error) {
	ctx, cancel := context.WithTimeout(ctx, s.cfg.ReceiptTimeout)
	defer cancel()

	hash := common.HexToHash(txHash)
	ticker := time.NewTicker(s.cfg.ReceiptPollInterval)
	defer ticker.Stop()

	for {
		receipt, err := s.client.TransactionReceipt(ctx, hash)
		if err == nil && receipt != nil {
			return &evmmech.TransactionReceipt{
				Status:      receipt.Status,
				BlockNumber: receipt.BlockNumber.Uint64(),
				TxHash:      receipt.TxHash.Hex(),
				Logs:        receipt.Logs,
			}, nil
		}
		if err != nil && !errors.Is(err, ethereum.NotFound) {
			// Transient RPC errors are expected while the tx propagates; keep
			// polling until the deadline rather than failing a paid settlement.
			_ = err
		}

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("receipt for %s not found within %s: %w", txHash, s.cfg.ReceiptTimeout, ctx.Err())
		case <-ticker.C:
		}
	}
}

// GetBalance returns the native balance when tokenAddress is empty or the zero
// address, and the ERC-20 balance otherwise.
func (s *Signer) GetBalance(ctx context.Context, address string, tokenAddress string) (*big.Int, error) {
	if tokenAddress == "" || tokenAddress == "0x0000000000000000000000000000000000000000" {
		balance, err := s.client.BalanceAt(ctx, common.HexToAddress(address), nil)
		if err != nil {
			return nil, fmt.Errorf("read native balance: %w", err)
		}
		return balance, nil
	}

	const erc20BalanceOfABI = `[{"constant":true,"inputs":[{"name":"account","type":"address"}],"name":"balanceOf","outputs":[{"name":"","type":"uint256"}],"type":"function"}]`
	result, err := s.ReadContract(ctx, tokenAddress, []byte(erc20BalanceOfABI), "balanceOf", common.HexToAddress(address))
	if err != nil {
		return nil, err
	}
	balance, ok := result.(*big.Int)
	if !ok {
		return nil, fmt.Errorf("unexpected balanceOf result type %T", result)
	}
	return balance, nil
}

// GetCode returns the bytecode at address; empty for an EOA.
func (s *Signer) GetCode(ctx context.Context, address string) ([]byte, error) {
	code, err := s.client.CodeAt(ctx, common.HexToAddress(address), nil)
	if err != nil {
		return nil, fmt.Errorf("read code: %w", err)
	}
	return code, nil
}

// Balance returns the settlement wallet's own native balance, implementing
// domain/wallet.BalanceReader for the gas monitor.
func (s *Signer) Balance(ctx context.Context) (wallet.Balance, error) {
	atomic, err := s.GetBalance(ctx, s.address.Hex(), "")
	if err != nil {
		return wallet.Balance{}, err
	}
	return wallet.Balance{
		Network:     s.network,
		Address:     s.address.Hex(),
		Atomic:      atomic,
		UnitDivisor: weiPerEther,
	}, nil
}

// feesPerGas builds EIP-1559 fee parameters, falling back to the legacy gas
// price on chains that do not report a base fee.
func (s *Signer) feesPerGas(ctx context.Context) (tip, feeCap *big.Int, err error) {
	head, headErr := s.client.HeaderByNumber(ctx, nil)
	if headErr == nil && head.BaseFee != nil {
		tip, err = s.client.SuggestGasTipCap(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("suggest gas tip: %w", err)
		}
		// Room for two base-fee increases (each block can rise 12.5%) so the
		// transaction stays includable through a short fee spike.
		feeCap = new(big.Int).Add(tip, new(big.Int).Mul(head.BaseFee, big.NewInt(2)))
	} else {
		gasPrice, priceErr := s.client.SuggestGasPrice(ctx)
		if priceErr != nil {
			return nil, nil, fmt.Errorf("suggest gas price: %w", priceErr)
		}
		tip, feeCap = gasPrice, gasPrice
	}

	if s.cfg.MaxFeePerGasWei != nil && feeCap.Cmp(s.cfg.MaxFeePerGasWei) > 0 {
		return nil, nil, fmt.Errorf("%w: %s wei > %s wei", ErrGasPriceTooHigh, feeCap, s.cfg.MaxFeePerGasWei)
	}
	if tip.Cmp(feeCap) > 0 {
		tip = feeCap
	}
	return tip, feeCap, nil
}

// estimateGas estimates the gas limit with a 25% buffer, falling back to the
// configured limit when estimation reverts (some settlement paths only become
// callable once the payment authorization is on-chain).
func (s *Signer) estimateGas(ctx context.Context, to common.Address, data []byte) uint64 {
	estimated, err := s.client.EstimateGas(ctx, ethereum.CallMsg{From: s.address, To: &to, Data: data})
	if err != nil || estimated == 0 {
		return s.cfg.GasLimit
	}
	buffered := estimated + estimated/4
	if buffered < s.cfg.GasLimit {
		return s.cfg.GasLimit
	}
	return buffered
}

// reserveNonce hands out the next nonce, resyncing with the node first so
// transactions sent by other processes using the same key are accounted for.
func (s *Signer) reserveNonce(ctx context.Context) (uint64, error) {
	s.nonceMu.Lock()
	defer s.nonceMu.Unlock()

	pending, err := s.client.PendingNonceAt(ctx, s.address)
	if err != nil {
		return 0, fmt.Errorf("read pending nonce: %w", err)
	}
	if !s.nonceValid || pending > s.nextNonce {
		s.nextNonce = pending
		s.nonceValid = true
	}

	nonce := s.nextNonce
	s.nextNonce++
	return nonce, nil
}

// invalidateNonce forces the next reservation to resync with the node.
func (s *Signer) invalidateNonce() {
	s.nonceMu.Lock()
	defer s.nonceMu.Unlock()
	s.nonceValid = false
}
