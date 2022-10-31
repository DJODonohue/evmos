package e2e

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/ory/dockertest/v3"
	"github.com/stretchr/testify/suite"

	"github.com/evmos/evmos/v9/tests/e2e/upgrade"
)

const (
	localRepository = "evmos"
	initialTag      = "initial"
	localVersionTag = "local"

	firstUpgradeHeight = 50
)

var (
	// common
	maxRetries = 10 // max retries for json unmarshalling
)

type upgradeParams struct {
	InitialVersion string
	TargetVersion  string
	MountPath      string

	MigrateGenesis bool
	SkipCleanup    bool
}

type IntegrationTestSuite struct {
	suite.Suite

	tmpDirs        []string
	upgradeManager *upgrade.Manager
	hermesResource *dockertest.Resource
	upgradeParams  upgradeParams
}

type status struct {
	LatestHeight string `json:"latest_block_height"`
}

type syncInfo struct {
	SyncInfo status `json:"SyncInfo"`
}

func TestIntegrationTestSuite(t *testing.T) {
	suite.Run(t, new(IntegrationTestSuite))
}

func (s *IntegrationTestSuite) SetupSuite() {
	s.T().Log("setting up e2e integration test suite...")
	var err error

	s.loadUpgradeParams()

	s.upgradeManager, err = upgrade.NewManager()
	s.NoError(err, "upgrade manager creation error")

}

func (s *IntegrationTestSuite) loadUpgradeParams() {
	preV := os.Getenv("INITIAL_VERSION")
	if preV == "" {
		s.Fail("no pre-upgrade version specified")
	}
	s.upgradeParams.InitialVersion = preV

	postV := os.Getenv("TARGET_VERSION")
	if postV == "" {
		s.Fail("no post-upgrade version specified")
	}
	s.upgradeParams.TargetVersion = postV

	migrateGenFlag := os.Getenv("MIGRATE_GENESIS")
	migrateGenesis, err := strconv.ParseBool(migrateGenFlag)
	s.Require().NoError(err, "invalid migrate genesis flag")
	s.upgradeParams.MigrateGenesis = migrateGenesis

	skipFlag := os.Getenv("E2E_SKIP_CLEANUP")
	skipCleanup, err := strconv.ParseBool(skipFlag)
	s.Require().NoError(err, "invalid skip cleanup flag")
	s.upgradeParams.SkipCleanup = skipCleanup

	mountPath := os.Getenv("MOUNT_PATH")
	if postV == "" {
		s.Fail("no mount path specified")
	}
	s.upgradeParams.MountPath = mountPath
	s.T().Log("upgrade params: ", s.upgradeParams)
}

func (s *IntegrationTestSuite) TearDownSuite() {
	if s.upgradeParams.SkipCleanup {
		return
	}
	s.T().Log("tearing down e2e integration test suite...")

	s.Require().NoError(s.upgradeManager.KillCurrentNode())

	s.Require().NoError(s.upgradeManager.RemoveNetwork())
	// TODO: cleanup ./build/

}

func (s *IntegrationTestSuite) runInitialNode() {
	err := s.upgradeManager.RunNode(localRepository, initialTag)
	s.NoError(err, "can't run initial node")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err = s.upgradeManager.WaitForHeight(ctx, 5)
	s.NoError(err)

	s.T().Logf("successfully started initial node version: %s", s.upgradeParams.InitialVersion)
}

func (s *IntegrationTestSuite) proposeUpgrade() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exec, err := s.upgradeManager.CreateSubmitProposalExec(
		ctx,
		s.upgradeParams.TargetVersion,
		firstUpgradeHeight,
	)
	s.NoError(err, "can't create submit proposal exec")
	outBuf, errBuf, err := s.upgradeManager.RunExec(ctx, exec)
	s.Require().NoErrorf(
		err,
		"failed to submit upgrade proposal; stdout: %s, stderr: %s", outBuf.String(), errBuf.String(),
	)

	s.Require().Truef(
		strings.Contains(outBuf.String(), "code: 0"),
		"tx returned non code 0"+outBuf.String(),
	)

	s.T().Logf("successfully submitted upgrade proposal")
}

func (s *IntegrationTestSuite) depositToProposal() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exec, err := s.upgradeManager.CreateDepositProposalExec(
		ctx,
	)
	s.NoError(err, "can't create deposit to proposal tx exec")
	outBuf, errBuf, err := s.upgradeManager.RunExec(ctx, exec)
	s.Require().NoErrorf(
		err,
		"failed to submit deposit to proposal tx; stdout: %s, stderr: %s", outBuf.String(), errBuf.String(),
	)

	s.Require().Truef(
		strings.Contains(outBuf.String(), "code: 0"),
		"tx returned non code 0"+outBuf.String(),
	)

	s.T().Logf("successfully deposited to proposal")
}

func (s *IntegrationTestSuite) voteForProposal() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exec, err := s.upgradeManager.CreateVoteProposalExec(
		ctx,
	)
	s.NoError(err, "can't create vote for proposal exec")
	outBuf, errBuf, err := s.upgradeManager.RunExec(ctx, exec)
	s.Require().NoErrorf(
		err,
		"failed to vote for proposal tx; stdout: %s, stderr: %s", outBuf.String(), errBuf.String(),
	)

	s.Require().Truef(
		strings.Contains(outBuf.String(), "code: 0"),
		"tx returned non code 0"+outBuf.String(),
	)

	s.T().Logf("successfully voted for upgrade proposal")
}

func (s *IntegrationTestSuite) upgrade() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := s.upgradeManager.WaitForHeight(ctx, firstUpgradeHeight)
	s.NoError(err, "can't reach upgrade height")
	buildeDir := strings.Split(s.upgradeParams.MountPath, ":")[0]
	err = s.upgradeManager.ExportState(buildeDir)
	s.NoError(err, "can't export node container state to local")

	err = s.upgradeManager.KillCurrentNode()
	s.NoError(err, "can't kill current node")

	err = s.upgradeManager.RunMountedNode(localRepository, localVersionTag, s.upgradeParams.MountPath)
	s.NoError(err, "can't mount and run upgraded node container")

	err = s.upgradeManager.WaitForHeight(ctx, firstUpgradeHeight+25)
	s.NoError(err, "node not produce blocks")

	s.T().Logf("successfully node for new version: %s", s.upgradeParams.TargetVersion)
}