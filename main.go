package main

import (
	"bufio"
	"chat-app/systems"
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"

	"github.com/pion/webrtc/v4"
)

var (
	SignalingServerAddress = "http://localhost:8080"
	StunServerAddress      = "stun1.l.google.com:19302"
	MaxPeers               = 4
)

func main() {
	AppMode := systems.DisplayModeOptions(SignalingServerAddress)
	var WebrtcConfiguration webrtc.Configuration = systems.CreateWebrtcConfiguration(StunServerAddress)
	switch AppMode {
	case systems.ModeHost:
		roomName, roomPassword := systems.DisplayRoomConfigOptions()

		hostSecret, err := systems.CreateRoom(roomName, roomPassword, SignalingServerAddress)
		if err != nil {
			log.Fatal(err)
		}

		go UpdateHost(hostSecret, roomName, WebrtcConfiguration)
		reader := bufio.NewReader(os.Stdin)

		fmt.Print("Enter your username: ")
		username, _ := reader.ReadString('\n')
		username = strings.Trim(username, "\n")
		ConnectToHost(roomName, roomPassword, username, WebrtcConfiguration)

	case systems.ModeJoin:
		roomName, roomPassword, username := systems.DisplayRoomJoinOptions()
		ConnectToHost(roomName, roomPassword, username, WebrtcConfiguration)

	}

}

type ConnectedPeer struct {
	peerId          string
	peerDescription systems.ServerPeerDescription
	peerConnection  *webrtc.PeerConnection
	dataChannel     *webrtc.DataChannel
}

func UpdateHost(hostSecret, roomName string, WebrtcConfiguration webrtc.Configuration) {
	// Initialize variables
	AllPeersDescription := make(chan map[string]systems.ServerPeerDescription)
	ConnectedPeers := make(map[string]*ConnectedPeer)
	var ConnectedPeersMutex sync.Mutex

	// Start polling server for peer descriptions
	go systems.PollUpdatedServerPeerDescriptions(AllPeersDescription, SignalingServerAddress, hostSecret, roomName)

	// Main loop to handle peer connections
	for {
		// Receive updated server peers
		serverPeers := <-AllPeersDescription

		// Iterate over each peer from the server
		for peerId, peer := range serverPeers {
			// Skip if the peer is already connected
			ConnectedPeersMutex.Lock()
			if _, exists := ConnectedPeers[peerId]; exists {
				ConnectedPeersMutex.Unlock()
				continue
			}

			// Initialize a new ConnectedPeer object
			ConnectedPeers[peerId] = &ConnectedPeer{}
			ConnectedPeersMutex.Unlock()

			// Handle connection with the new peer in a separate goroutine
			go func() {
				// Create an answer peer connection
				answerSDP, pendingCandidates, peerConnection := systems.CreateAnswerRTCPeerConnection(WebrtcConfiguration, peer)

				// Send the answer to the signaling server
				systems.SendAnswerToServer(answerSDP, pendingCandidates, roomName, hostSecret, peerId, SignalingServerAddress)

				// Lock and update the peer connection
				ConnectedPeersMutex.Lock()
				ConnectedPeers[peerId].peerConnection = peerConnection
				ConnectedPeersMutex.Unlock()

				// Set up the data channel and message forwarding
				peerConnection.OnDataChannel(func(dc *webrtc.DataChannel) {
					ConnectedPeersMutex.Lock()
					ConnectedPeers[peerId].dataChannel = dc
					ConnectedPeersMutex.Unlock()

					// Forward messages to all connected peers
					dc.OnMessage(func(msg webrtc.DataChannelMessage) {
						ConnectedPeersMutex.Lock()
						defer ConnectedPeersMutex.Unlock()
						for _, connectedPeer := range ConnectedPeers {
							if connectedPeer.dataChannel != nil {
								err := connectedPeer.dataChannel.SendText(string(msg.Data))
								if err != nil {
									log.Printf("Failed to forward message to peer: %v", err)
								}
							}
						}
					})
				})
			}()
		}
	}
}
func ConnectToHost(roomName, roomPassword, username string, WebrtcConfiguration webrtc.Configuration) {
	// gather ice candidates and create offer sdp
	MessageHistory := ""

	offer, pendingCandidates, peerConnection, dataChannel := systems.CreateOfferRTCPeerConnection(WebrtcConfiguration)

	peerSecret := systems.SendOfferToServer(offer, pendingCandidates, roomName, roomPassword, username, SignalingServerAddress)

	dataChannel.OnMessage(func(msg webrtc.DataChannelMessage) {
		MessageHistory += fmt.Sprintln(string(msg.Data))

		ClearScreen()
		fmt.Println(MessageHistory)
		fmt.Print("Type a message: ")
	})

	answerSdp, answerIceCandidates := systems.PollServerAnswer(SignalingServerAddress, roomName, peerSecret, username)

	for _, candidate := range answerIceCandidates {
		peerConnection.AddICECandidate(webrtc.ICECandidateInit{Candidate: candidate})
	}
	err := peerConnection.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer,
		SDP:  answerSdp,
	})
	if err != nil {
		log.Fatal("Could not set remote description on client: ", err)
	}
	for {

		fmt.Println(MessageHistory)
		message := systems.AskForMessageInput()
		if err := dataChannel.SendText(username + ": " + message); err != nil {
			fmt.Println(err)
		}
	}
}

func ClearScreen() {
	if runtime.GOOS == "windows" {
		cmd := exec.Command("cmd", "/c", "cls")
		cmd.Stdout = os.Stdout
		cmd.Run()
	} else {
		fmt.Print("\033[2J\033[H") // Clear the screen and reset the cursor position
	}

}
