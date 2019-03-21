package dkgnode

import (
	"math/big"
	"strconv"
	"time"

	"github.com/torusresearch/torus-public/logging"
)

type NodeListUpdates struct {
	Type    string
	Payload interface{}
}

type KeyGenUpdates struct {
	Type    string
	Payload interface{}
}

func startKeyGenerationMonitor(suite *Suite, keyGenMonitorUpdates chan KeyGenUpdates) {
	for {
		logging.Debugf("KEYGEN: in start keygen monitor %s", suite.ABCIApp.state.LocalStatus)
		time.Sleep(1 * time.Second)
		// if suite.ABCIApp.state.LocalStatus["all_initiate_keygen"] != "" {
		var localStatus = suite.ABCIApp.state.LocalStatus.Current()
		if localStatus == "running_keygen" || localStatus == "verifying_shares" {
			logging.Debugf("KEYGEN: WAITING FOR ALL INITIATE KEYGEN TO STOP BEING IN PROGRESS %s", suite.ABCIApp.state.LocalStatus)
			continue
		} else {
			logging.Debugf("KEYGEN: KEYGEN NOT IN PROGRESS %s", localStatus)
		}

		percentLeft := 100 * (suite.ABCIApp.state.LastCreatedIndex - suite.ABCIApp.state.LastUnassignedIndex) / uint(suite.Config.KeysPerEpoch)
		if percentLeft > uint(suite.Config.KeyBufferTriggerPercentage) {
			logging.Debugf("KEYGEN: keygeneration trigger percent left not reached %d", percentLeft)
			continue
		}
		startingIndex := int(suite.ABCIApp.state.LastCreatedIndex)
		endingIndex := suite.Config.KeysPerEpoch + int(suite.ABCIApp.state.LastCreatedIndex)

		logging.Debugf("KEYGEN: we are starting keygen %v", suite.ABCIApp.state.LocalStatus)
		nodeIndex, err := matchNode(suite, suite.EthSuite.NodePublicKey.X.Text(16), suite.EthSuite.NodePublicKey.Y.Text(16))
		if err != nil {
			logging.Errorf("KEYGEN: could not get nodeIndex %s", err)
			continue
		}
		go listenForShares(suite, suite.Config.KeysPerEpoch*suite.Config.NumberOfNodes*suite.Config.NumberOfNodes)
		for {
			time.Sleep(1 * time.Second)
			initiateKeyGenerationStatusWrapper := DefaultBFTTxWrapper{
				StatusBFTTx{
					FromPubKeyX: suite.EthSuite.NodePublicKey.X.Text(16),
					FromPubKeyY: suite.EthSuite.NodePublicKey.Y.Text(16),
					Epoch:       suite.ABCIApp.state.Epoch,
					StatusType:  "initiate_keygen",
					StatusValue: "Y",
					Data:        []byte(strconv.Itoa(endingIndex)),
				},
			}
			_, err := suite.BftSuite.BftRPC.Broadcast(initiateKeyGenerationStatusWrapper)
			if err != nil {
				logging.Errorf("KEYGEN: could not broadcast initiateKeygeneration %s", err)
			}

			fsm, fsmExists := suite.ABCIApp.state.NodeStatus[uint(nodeIndex)]
			allInitiateStatus := suite.ABCIApp.state.LocalStatus.Current()

			// TODO: expecting keygen process to take longer than 1 second
			if (fsmExists && fsm.Current() == "initiated_keygen") || (allInitiateStatus == "ready_for_keygen" || allInitiateStatus == "running_keygen") {
				break
			}
		}
		for {
			logging.Debugf("KEYGEN: WAITING FOR ALL INITIATE KEYGEN TO BE Y %v", suite.ABCIApp.state.LocalStatus)
			logging.Debugf("%v", suite.ABCIApp.state)
			time.Sleep(1 * time.Second)
			// if suite.ABCIApp.state.LocalStatus["all_initiate_keygen"] == "Y" {
			if suite.ABCIApp.state.LocalStatus.Current() == "ready_for_keygen" {
				logging.Debugf("STATUSTX: localstatus all initiate keygen is Y, appstate s%", suite.ABCIApp.state.LocalStatus)
				//reset keygen flag
				suite.ABCIApp.state.LocalStatus.Event("start_keygen")
				//report back to main process
				keyGenMonitorUpdates <- KeyGenUpdates{
					Type:    "start_keygen",
					Payload: []int{startingIndex, endingIndex},
				}
				break
			}
		}

	}
}

func startNodeListMonitor(suite *Suite, tickerChan <-chan time.Time, nodeListUpdates chan NodeListUpdates) {
	logging.Info("Started Node List Monitor")
	for _ = range tickerChan {
		logging.Debug("Checking Node List...")
		// Fetch Node List from contract address
		//TODO: make epoch vairable
		epoch := big.NewInt(int64(0))
		ethList, positions, err := suite.EthSuite.NodeListContract.ViewNodes(nil, epoch)
		// If we can't reach ethereum node, lets try next time
		if err != nil {
			logging.Errorf("Could not View Nodes on ETH Network %s", err)
		} else {
			// Build count of nodes connected to
			logging.Debugf("Indexes %v %v", positions, ethList)
			connectedNodes := 0
			nodeList := make([]*NodeReference, len(ethList))
			if len(ethList) > 0 {
				for _, ethListNode := range ethList {
					// Check if node is online by pinging
					temp, err := connectToJSONRPCNode(suite, *epoch, ethListNode)
					if err != nil {
						logging.Errorf("%v", err)
					}

					if temp != nil {
						if nodeList[int(temp.Index.Int64())-1] == nil { // TODO: use mapping
							nodeList[int(temp.Index.Int64())-1] = temp
						}
						connectedNodes++
					}
				}
			}
			//if we've connected to all nodes we send back the most recent list
			if connectedNodes == len(ethList) {
				nodeListUpdates <- NodeListUpdates{"update", nodeList}
			}
		}
	}

}
