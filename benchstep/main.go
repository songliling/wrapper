package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/filecoin-project/go-state-types/abi"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/util"
	"github.com/tjan147/wrapper"
	"math/rand"
	"os"
	"path"
	"strings"
	"time"
)

const (
	StepDBName = "bench"
	Step1      = "step1"
	Step2      = "step2"
)

var (
	DB           *leveldb.DB
	ValidatorKey = []byte{0x01}
	MinerKey     = []byte{0x10}
	DirKey       = []byte{0x20}
	StatementKey = []byte{0x30}
)

func getFileKey(givenID []byte) []byte {
	return append(DirKey, givenID...)
}

func getStatementKey(ID []byte) []byte {
	return append(StatementKey, ID...)
}

func getRandStatementID() abi.SealRandomness {
	ret := make([]byte, 32)
	if _, err := rand.Read(ret); err != nil {
		panic(err)
	}

	return abi.SealRandomness(ret)
}

func inputToProofType(input string) abi.RegisteredSealProof {
	switch strings.ToUpper(strings.TrimSpace(input)) {
	case "2K":
		return abi.RegisteredSealProof_StackedDrg2KiBV1
	case "8M":
		return abi.RegisteredSealProof_StackedDrg8MiBV1
	case "512M":
		return abi.RegisteredSealProof_StackedDrg512MiBV1
	case "32G":
		return abi.RegisteredSealProof_StackedDrg32GiBV1
	}

	fmt.Printf("Unknown sector size %s, replaced with 2K as input\n", input)
	return abi.RegisteredSealProof_StackedDrg2KiBV1
}

func init() {
	db, err := leveldb.OpenFile(StepDBName, nil)
	if err != nil {
		panic(fmt.Sprintf("open leveldb error: %s", err.Error()))
	}
	DB = db
}

func cleanDB() {
	DB.Delete(MinerKey, nil)
	DB.Delete(ValidatorKey, nil)
	iteratorDir := DB.NewIterator(util.BytesPrefix(DirKey), nil)
	for iteratorDir.Next() {
		DB.Delete(iteratorDir.Key(), nil)
	}
	iteratorDir.Release()

	iteratorSt := DB.NewIterator(util.BytesPrefix(StatementKey), nil)
	for iteratorSt.Next() {
		DB.Delete(iteratorSt.Key(), nil)
	}
	iteratorSt.Release()
}

func setMiner(miner wrapper.Miner) error {
	mBytes, err := json.Marshal(miner)
	if err != nil {
		return err
	}
	for k, dir := range miner.Store.FileDB {
		bytes, _ := base64.StdEncoding.DecodeString(k)
		if err := DB.Put(getFileKey(bytes), []byte(dir), nil); err != nil {
			return err
		}
	}

	for _, st := range miner.Store.StatementDB {
		stBytes, err := json.Marshal(st)
		if err != nil {
			return err
		}
		if err = DB.Put(getStatementKey(st.ID), stBytes, nil); err != nil {
			return err
		}
	}
	return DB.Put(MinerKey, mBytes, nil)
}

func getMiner() (*wrapper.Miner, error) {
	value, err := DB.Get(MinerKey, nil)
	if value == nil || err != nil {
		return nil, fmt.Errorf("get miner error")
	}
	var miner wrapper.Miner
	if err = json.Unmarshal(value, &miner); err != nil {
		return nil, err
	}
	miner.Store = wrapper.NewMinerStorage()

	iteratorDir := DB.NewIterator(util.BytesPrefix(DirKey), nil)
	for iteratorDir.Next() {
		miner.Store.FileDB[base64.StdEncoding.EncodeToString(iteratorDir.Key()[1:])] = string(iteratorDir.Value())
	}
	iteratorDir.Release()

	iteratorSt := DB.NewIterator(util.BytesPrefix(StatementKey), nil)
	for iteratorSt.Next() {
		var st wrapper.Statement
		if err := json.Unmarshal(iteratorSt.Value(), &st); err != nil {
			return nil, err
		}
		miner.Store.StatementDB[base64.StdEncoding.EncodeToString(iteratorSt.Key()[1:])] = &st
	}
	iteratorSt.Release()

	return &miner, nil
}

func setValidator(validator wrapper.Validator) error {
	vBytes, err := json.Marshal(validator)
	if err != nil {
		return err
	}

	return DB.Put(ValidatorKey, vBytes, nil)
}

func getValidator() (*wrapper.Validator, error) {
	value, err := DB.Get(ValidatorKey, nil)
	if value == nil || err != nil {
		return nil, fmt.Errorf("get validator error")
	}
	var v wrapper.Validator
	if err = json.Unmarshal(value, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

func step1(dir string, typ abi.RegisteredSealProof) {
	defer DB.Close()
	cleanDB()
	// seed the randomizer
	rand.Seed(time.Now().UnixNano())

	// initialize for the PoRep process
	typSize, err := typ.SectorSize()
	if err != nil {
		panic(err)
	}

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		os.RemoveAll(dir)
	}
	if err := os.Mkdir(dir, 0755); err != nil {
		panic(err)
	}

	fakePiece := path.Join(dir, "fakepiece.dat")
	if err := wrapper.CreateFakeDataFile(fakePiece, uint64(wrapper.UnpaddedSpace(uint64(typSize)))); err != nil {
		panic(err)
	}

	// create the report instance
	report, err := wrapper.NewReport(dir, typ)
	if err != nil {
		panic(err)
	}

	// prepare the roles
	validator := wrapper.NewValidator()
	miner, err := wrapper.NewMiner(rand.Int63(), typ)
	if err != nil {
		panic(err)
	}
	// MINER pledges to the VALIDATOR
	miner.Pledge(validator)

	// assemble
	step := wrapper.NewStepMeasure("Assemble")
	staged, _, _, err := miner.InitSectorDir(dir)
	if err != nil {
		panic(err)
	}
	_, _, pieceInfos, err := miner.AssemblePieces(staged, []string{fakePiece})
	if err != nil {
		panic(err)
	}
	staged.Close()
	report.AddStep(step.Done())

	// setup
	step = wrapper.NewStepMeasure("Setup")
	statement := miner.CommitStatement(
		getRandStatementID(),
		rand.Uint64(),
		dir,
		pieceInfos,
	)
	report.AddStep(step.Done())
	report.DumpStep(dir, Step1)

	validator.HandlePoRepStatement(statement)

	// persist validator and miner
	if err := setMiner(*miner); err != nil {
		fmt.Printf("save miner error: %s \n", err.Error())
		os.Exit(0)
	}

	if err = setValidator(*validator); err != nil {
		fmt.Printf("save validator error: %s \n", err.Error())
		os.Exit(0)
	}
}

func step2(dir string, typ abi.RegisteredSealProof) {
	defer DB.Close()
	report, err := wrapper.NewReport(dir, typ)
	if err != nil {
		panic(err)
	}
	miner, err := getMiner()
	if err != nil {
		panic(err)
	}
	validator, err := getValidator()
	if err != nil {
		panic(err)
	}
	fmt.Println("statementID", base64.StdEncoding.EncodeToString(validator.Keeper.Statement.ID))
	validator.GenChallenge()
	miner.Validator = validator

	// prove
	step := wrapper.NewStepMeasure("Prove")
	challenge := miner.QueryChallengeSet()
	proof := miner.ResponseToChallenge(challenge)
	report.AddStep(step.Done())

	// verify
	step = wrapper.NewStepMeasure("Verify")
	isValid, err := validator.HandlePoRepProof(proof)
	if err != nil {
		panic(err)
	}
	if !isValid {
		panic(fmt.Errorf("porep verification failed"))
	}
	report.AddStep(step.Done())

	// dump report
	if err := report.DumpStep(dir, Step2); err != nil {
		panic(err)
	}
}

func main() {
	// validate the input parameters
	if len(os.Args) != 4 {
		fmt.Println("Require 2 arguments as input parameters.")
		fmt.Println("Example:")
		fmt.Printf("\t%s sample 2k step1|step2 \n", os.Args[0])
		os.Exit(0)
	}
	dir := strings.TrimSpace(os.Args[1])
	typ := inputToProofType(os.Args[2])

	switch os.Args[3] {
	case Step1:
		step1(dir, typ)
	case Step2:
		step2(dir, typ)
	default:
		fmt.Printf("invalid value: %s, expect step1|step2 \n", os.Args[3])
		os.Exit(0)
	}
	// seed the randomizer
}
