// matrix-pulsesms - A Matrix-PulseSMS puppeting bridge.
// Copyright (C) 2021 Cam Sweeney
// Copyright (C) 2020 Tulir Asokan
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package main

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"maunium.net/go/maulogger/v2"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/appservice"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"
	"maunium.net/go/mautrix/id"

	"github.com/yawks/pulsesms"
)

type CommandHandler struct {
	bridge *Bridge
	log    maulogger.Logger
}

// NewCommandHandler creates a CommandHandler
func NewCommandHandler(bridge *Bridge) *CommandHandler {
	return &CommandHandler{
		bridge: bridge,
		log:    bridge.Log.Sub("Command handler"),
	}
}

// CommandEvent stores all data which might be used to handle commands
type CommandEvent struct {
	Bot     *appservice.IntentAPI
	Bridge  *Bridge
	Portal  *Portal
	Handler *CommandHandler
	RoomID  id.RoomID
	User    *User
	Command string
	Args    []string
}

// Reply sends a reply to command as notice
func (ce *CommandEvent) Reply(msg string, args ...interface{}) {
	content := format.RenderMarkdown(fmt.Sprintf(msg, args...), true, false)
	content.MsgType = event.MsgNotice
	intent := ce.Bot
	if ce.Portal != nil && ce.Portal.IsPrivateChat() {
		intent = ce.Portal.MainIntent()
	}
	_, err := intent.SendMessageEvent(ce.RoomID, event.EventMessage, content)
	if err != nil {
		ce.Handler.log.Warnfln("Failed to reply to command from %s: %v", ce.User.MXID, err)
	}
}

// Handle handles messages to the bridge
func (handler *CommandHandler) Handle(roomID id.RoomID, user *User, message string) {
	args := strings.Fields(message)
	if len(args) == 0 {
		args = []string{"unknown-command"}
	}
	ce := &CommandEvent{
		Bot:     handler.bridge.Bot,
		Bridge:  handler.bridge,
		Portal:  handler.bridge.GetPortalByMXID(roomID),
		Handler: handler,
		RoomID:  roomID,
		User:    user,
		Command: strings.ToLower(args[0]),
		Args:    args[1:],
	}
	if roomID == handler.bridge.Config.Bridge.Relaybot.ManagementRoom {
		handler.CommandRelaybot(ce)
	} else {
		handler.CommandMux(ce)
	}
}

func (handler *CommandHandler) CommandMux(ce *CommandEvent) {
	switch ce.Command {
	case "relaybot":
		handler.CommandRelaybot(ce)
	case "login":
		handler.CommandLogin(ce)
	case "logout-matrix":
		handler.CommandLogoutMatrix(ce)
	case "help":
		handler.CommandHelp(ce)
	case "version":
		handler.CommandVersion(ce)
	case "disconnect":
		handler.CommandDisconnect(ce)
	case "ping":
		handler.CommandPing(ce)
	case "delete-connection":
		handler.CommandDeleteConnection(ce)
	case "delete-session":
		handler.CommandDeleteSession(ce)
	case "delete-portal":
		handler.CommandDeletePortal(ce)
	case "delete-all-portals":
		handler.CommandDeleteAllPortals(ce)
	case "discard-megolm-session", "discard-session":
		handler.CommandDiscardMegolmSession(ce)
	case "dev-test":
		handler.CommandDevTest(ce)
	case "set-pl":
		handler.CommandSetPowerLevel(ce)
	case "logout":
		handler.CommandLogout(ce)
	case "toggle":
		handler.CommandToggle(ce)
	case "login-matrix", "sync", "list", "open", "pm", "invite-link", "join", "create", "search":
		if !ce.User.HasSession() {
			ce.Reply("You are not logged in. Use the `login` command to log into PulseSMS.")
			return
		} else if !ce.User.IsConnected() {
			ce.Reply("You are not connected to PulseSMS. Use the `reconnect` command to reconnect.")
			return
		}

		switch ce.Command {
		case "login-matrix":
			handler.CommandLoginMatrix(ce)
		case "sync":
			handler.CommandSync(ce)
		case "list":
			handler.CommandList(ce)
		case "open":
			handler.CommandOpen(ce)
		case "pm":
			handler.CommandPM(ce)
		case "search":
			handler.CommandSearch(ce)
		// case "invite-link":
		// 	handler.CommandInviteLink(ce)
		// case "join":
		// 	handler.CommandJoin(ce)
		case "create":
			handler.CommandCreate(ce)
		}
	default:
		ce.Reply("Unknown command, use the `help` command for help.")
	}
}

func (handler *CommandHandler) CommandDiscardMegolmSession(ce *CommandEvent) {
	if handler.bridge.Crypto == nil {
		ce.Reply("This bridge instance doesn't have end-to-bridge encryption enabled")
	} else if !ce.User.Admin {
		ce.Reply("Only the bridge admin can reset Megolm sessions")
	} else {
		handler.bridge.Crypto.ResetSession(ce.RoomID)
		ce.Reply("Successfully reset Megolm session in this room. New decryption keys will be shared the next time a message is sent from PulseSMS.")
	}
}

func (handler *CommandHandler) CommandRelaybot(ce *CommandEvent) {
	if handler.bridge.Relaybot == nil {
		ce.Reply("The relaybot is disabled")
	} else if !ce.User.Admin {
		ce.Reply("Only admins can manage the relaybot")
	} else {
		if ce.Command == "relaybot" {
			if len(ce.Args) == 0 {
				ce.Reply("**Usage:** `relaybot <command>`")
				return
			}
			ce.Command = strings.ToLower(ce.Args[0])
			ce.Args = ce.Args[1:]
		}
		ce.User = handler.bridge.Relaybot
		handler.CommandMux(ce)
	}
}

func (handler *CommandHandler) CommandDevTest(_ *CommandEvent) {

}

const cmdVersionHelp = `version - View the bridge version`

func (handler *CommandHandler) CommandVersion(ce *CommandEvent) {
	linkifiedVersion := fmt.Sprintf("v%s", Version)
	if Tag == Version {
		linkifiedVersion = fmt.Sprintf("[v%s](%s/releases/v%s)", Version, URL, Tag)
	} else if len(Commit) > 8 {
		linkifiedVersion = strings.Replace(linkifiedVersion, Commit[:8], fmt.Sprintf("[%s](%s/commit/%s)", Commit[:8], URL, Commit), 1)
	}
	ce.Reply(fmt.Sprintf("[%s](%s) %s (%s)", Name, URL, linkifiedVersion, BuildTime))
}

const cmdInviteLinkHelp = `invite-link - Get an invite link to the current group chat.`

// func (handler *CommandHandler) CommandInviteLink(ce *CommandEvent) {
// 	if ce.Portal == nil {
// 		ce.Reply("Not a portal room")
// 		return
// 	} else if ce.Portal.IsPrivateChat() {
// 		ce.Reply("Can't get invite link to private chat")
// 		return
// 	}

// 	link, err := ce.User.Conn.GroupInviteLink(ce.Portal.Key.PID)
// 	if err != nil {
// 		ce.Reply("Failed to get invite link: %v", err)
// 		return
// 	}
// 	ce.Reply("%s%s", inviteLinkPrefix, link)
// }

const cmdJoinHelp = `join <invite link> - Join a group chat with an invite link.`

// func (handler *CommandHandler) CommandJoin(ce *CommandEvent) {
// 	if len(ce.Args) == 0 {
// 		ce.Reply("**Usage:** `join <invite link>`")
// 		return
// 	} else if len(ce.Args[0]) <= len(inviteLinkPrefix) || ce.Args[0][:len(inviteLinkPrefix)] != inviteLinkPrefix {
// 		ce.Reply("That doesn't look like a WhatsApp invite link")
// 		return
// 	}

// 	pid, err := ce.User.Conn.GroupAcceptInviteCode(ce.Args[0][len(inviteLinkPrefix):])
// 	if err != nil {
// 		ce.Reply("Failed to join group: %v", err)
// 		return
// 	}

// 	handler.log.Debugln("%s successfully joined group %s", ce.User.MXID, pid)
// 	portal := handler.bridge.GetPortalByPID(database.GroupPortalKey(pid))
// 	if len(portal.MXID) > 0 {
// 		portal.Sync(ce.User, pulsesms.Contact{PID: portal.Key.PID})
// 		ce.Reply("Successfully joined group \"%s\" and synced portal room: [%s](https://matrix.to/#/%s)", portal.Name, portal.Name, portal.MXID)
// 	} else {
// 		err = portal.CreateMatrixRoom(ce.User)
// 		if err != nil {
// 			ce.Reply("Failed to create portal room: %v", err)
// 			return
// 		}

// 		ce.Reply("Successfully joined group \"%s\" and created portal room: [%s](https://matrix.to/#/%s)", portal.Name, portal.Name, portal.MXID)
// 	}
// }

const cmdCreateHelp = `create - Create a group chat.`

func (handler *CommandHandler) CommandCreate(ce *CommandEvent) {
	if ce.Portal != nil {
		ce.Reply("This is already a portal room")
		return
	}

	ce.Reply("not implemented")
	return

	// members, err := ce.Bot.JoinedMembers(ce.RoomID)
	// if err != nil {
	// 	ce.Reply("Failed to get room members: %v", err)
	// 	return
	// }

	// var roomNameEvent event.RoomNameEventContent
	// err = ce.Bot.StateEvent(ce.RoomID, event.StateRoomName, "", &roomNameEvent)
	// if err != nil && !errors.Is(err, mautrix.MNotFound) {
	// 	ce.Reply("Failed to get room name")
	// 	return
	// } else if len(roomNameEvent.Name) == 0 {
	// 	ce.Reply("Please set a name for the room first")
	// 	return
	// }

	// var encryptionEvent event.EncryptionEventContent
	// err = ce.Bot.StateEvent(ce.RoomID, event.StateEncryption, "", &encryptionEvent)
	// if err != nil && !errors.Is(err, mautrix.MNotFound) {
	// 	ce.Reply("Failed to get room encryption status")
	// 	return
	// }

	// participants := []string{ce.User.PID}
	// for userID := range members.Joined {
	// 	pid, ok := handler.bridge.ParsePuppetMXID(userID)
	// 	if ok && pid != ce.User.PID {
	// 		participants = append(participants, pid)
	// 	}
	// }

	// resp, err := ce.User.Conn.CreateGroup(roomNameEvent.Name, participants)
	// if err != nil {
	// 	ce.Reply("Failed to create group: %v", err)
	// 	return
	// }
	// portal := handler.bridge.GetPortalByPID(database.GroupPortalKey(resp.GroupID))
	// portal.roomCreateLock.Lock()
	// defer portal.roomCreateLock.Unlock()
	// if len(portal.MXID) != 0 {
	// 	portal.log.Warnln("Detected race condition in room creation")
	// 	// TODO race condition, clean up the old room
	// }
	// portal.MXID = ce.RoomID
	// portal.Name = roomNameEvent.Name
	// portal.Encrypted = encryptionEvent.Algorithm == id.AlgorithmMegolmV1
	// if !portal.Encrypted && handler.bridge.Config.Bridge.Encryption.Default {
	// 	_, err = portal.MainIntent().SendStateEvent(portal.MXID, event.StateEncryption, "", &event.EncryptionEventContent{Algorithm: id.AlgorithmMegolmV1})
	// 	if err != nil {
	// 		portal.log.Warnln("Failed to enable e2be:", err)
	// 	}
	// 	portal.Encrypted = true
	// }

	// portal.Update()
	// portal.UpdateBridgeInfo()

	// ce.Reply("Successfully created WhatsApp group %s", portal.Key.PID)
	// inCommunity := ce.User.addPortalToCommunity(portal)
	// ce.User.CreateUserPortal(database.PortalKeyWithMeta{PortalKey: portal.Key, InCommunity: inCommunity})
}

const cmdSetPowerLevelHelp = `set-pl [user ID] <power level> - Change the power level in a portal room. Only for bridge admins.`

func (handler *CommandHandler) CommandSetPowerLevel(ce *CommandEvent) {
	if ce.Portal == nil {
		ce.Reply("Not a portal room")
		return
	}
	var level int
	var userID id.UserID
	var err error
	if len(ce.Args) == 1 {
		level, err = strconv.Atoi(ce.Args[0])
		if err != nil {
			ce.Reply("Invalid power level \"%s\"", ce.Args[0])
			return
		}
		userID = ce.User.MXID
	} else if len(ce.Args) == 2 {
		userID = id.UserID(ce.Args[0])
		_, _, err := userID.Parse()
		if err != nil {
			ce.Reply("Invalid user ID \"%s\"", ce.Args[0])
			return
		}
		level, err = strconv.Atoi(ce.Args[1])
		if err != nil {
			ce.Reply("Invalid power level \"%s\"", ce.Args[1])
			return
		}
	} else {
		ce.Reply("**Usage:** `set-pl [user] <level>`")
		return
	}
	intent := ce.Portal.MainIntent()
	_, err = intent.SetPowerLevel(ce.RoomID, userID, level)
	if err != nil {
		ce.Reply("Failed to set power levels: %v", err)
	}
}

const cmdLoginHelp = `login - Authenticate this Bridge as PulseSMS Web Client`

// CommandLogin handles login command
func (handler *CommandHandler) CommandLogin(ce *CommandEvent) {
	ce.User.Login(ce)

	if !ce.User.Connect(true) {
		ce.User.log.Debugln("Connect() returned false, assuming error was logged elsewhere and canceling login.")
		return
	}
}

const cmdLogoutHelp = `logout - Logout from PulseSMS`

// CommandLogout handles !logout command
func (handler *CommandHandler) CommandLogout(ce *CommandEvent) {
	if ce.User.Session == nil {
		ce.Reply("You're not logged in.")
		return
	} else if !ce.User.IsConnected() {
		ce.Reply("You are not connected to PulseSMS. Use the `reconnect` command to reconnect, or `delete-session` to forget all login information.")
		return
	}
	puppet := handler.bridge.GetPuppetByPID(ce.User.PID)
	if puppet.CustomMXID != "" {
		err := puppet.SwitchCustomMXID("", "")
		if err != nil {
			ce.User.log.Warnln("Failed to logout-matrix while logging out of PulseSMS:", err)
		}
	}
	ce.User.removeFromPIDMap()
	// TODO this causes a foreign key violation, which should be fixed
	//ce.User.PID = ""
	ce.User.SetSession(nil)
	ce.User.DeleteConnection()
	ce.Reply("Logged out successfully.")
}

const cmdToggleHelp = `toggle <presence|receipts|all> - Toggle bridging of presence or read receipts`

func (handler *CommandHandler) CommandToggle(ce *CommandEvent) {
	if len(ce.Args) == 0 || (ce.Args[0] != "presence" && ce.Args[0] != "receipts" && ce.Args[0] != "all") {
		ce.Reply("**Usage:** `toggle <presence|receipts|all>`")
		return
	}
	if ce.User.Session == nil {
		ce.Reply("You're not logged in.")
		return
	}
	customPuppet := handler.bridge.GetPuppetByCustomMXID(ce.User.MXID)
	if customPuppet == nil {
		ce.Reply("You're not logged in with your Matrix account.")
		return
	}

	customPuppet.Update()
}

const cmdDeleteSessionHelp = `delete-session - Delete session information and disconnect from PulseSMS without sending a logout request`

func (handler *CommandHandler) CommandDeleteSession(ce *CommandEvent) {
	if ce.User.Session == nil && ce.User.Pulse == nil {
		ce.Reply("Nothing to purge: no session information stored and no active connection.")
		return
	}
	//ce.User.PID = ""
	ce.User.removeFromPIDMap()
	ce.User.SetSession(nil)
	ce.User.DeleteConnection()
	ce.Reply("Session information purged")
}

const cmdDeleteConnectionHelp = `delete-connection - Disconnect ignoring errors and delete internal connection state.`

func (handler *CommandHandler) CommandDeleteConnection(ce *CommandEvent) {
	if ce.User.Pulse == nil {
		ce.Reply("You don't have a PulseSMS connection.")
		return
	}
	ce.User.DeleteConnection()
	ce.Reply("Successfully disconnected. Use the `reconnect` command to reconnect.")
}

const cmdDisconnectHelp = `disconnect - Disconnect from PulseSMS (without logging out)`

func (handler *CommandHandler) CommandDisconnect(ce *CommandEvent) {
	if ce.User.Pulse == nil {
		ce.Reply("You don't have a PulseSMS connection.")
		return
	}
	ce.User.Pulse.Disconnect()
	ce.User.bridge.Metrics.TrackConnectionState(ce.User.PID, false)
	ce.User.sendBridgeStatus(AsmuxPong{Error: AsmuxWANotConnected})
	ce.Reply("Successfully disconnected. Use the `login` command to reconnect.")
}

const cmdPingHelp = `ping - Check your connection to PulseSMS.`

func (handler *CommandHandler) CommandPing(ce *CommandEvent) {
	if ce.User.Session == nil {
		if ce.User.IsLoginInProgress() {
			ce.Reply("You're not logged into PulseSMS, but there's a login in progress.")
		} else {
			ce.Reply("You're not logged into PulseSMS.")
		}
	} else if ce.User.Pulse == nil {
		ce.Reply("You don't have a PulseSMS connection.")
		// } else if err := ce.User.Pulse.AdminTest(); err != nil {
	} else if !ce.User.Pulse.IsConnected() {
		if ce.User.IsLoginInProgress() {
			ce.Reply("Connection not OK:, but login in progress")
		} else {
			ce.Reply("Connection not OK:")
		}
	} else {
		ce.Reply("Connection to PulseSMS OK")
	}
}

const cmdHelpHelp = `help - Prints this help`

// CommandHelp handles help command
func (handler *CommandHandler) CommandHelp(ce *CommandEvent) {
	cmdPrefix := ""
	if ce.User.ManagementRoom != ce.RoomID || ce.User.IsRelaybot {
		cmdPrefix = handler.bridge.Config.Bridge.CommandPrefix + " "
	}

	ce.Reply("* " + strings.Join([]string{
		cmdPrefix + cmdHelpHelp,
		cmdPrefix + cmdVersionHelp,
		cmdPrefix + cmdLoginHelp,
		cmdPrefix + cmdLogoutHelp,
		cmdPrefix + cmdDeleteSessionHelp,
		cmdPrefix + cmdDisconnectHelp,
		cmdPrefix + cmdDeleteConnectionHelp,
		cmdPrefix + cmdPingHelp,
		cmdPrefix + cmdLoginMatrixHelp,
		cmdPrefix + cmdLogoutMatrixHelp,
		cmdPrefix + cmdToggleHelp,
		cmdPrefix + cmdSyncHelp,
		cmdPrefix + cmdListHelp,
		cmdPrefix + cmdSearchHelp,
		cmdPrefix + cmdOpenHelp,
		cmdPrefix + cmdPMHelp,
		cmdPrefix + cmdInviteLinkHelp,
		cmdPrefix + cmdJoinHelp,
		cmdPrefix + cmdCreateHelp,
		cmdPrefix + cmdSetPowerLevelHelp,
		cmdPrefix + cmdDeletePortalHelp,
		cmdPrefix + cmdDeleteAllPortalsHelp,
	}, "\n* "))
}

const cmdSyncHelp = `sync [--create-all] - Synchronize contacts from phone and optionally create portals for group chats.`

// CommandSync handles sync command
func (handler *CommandHandler) CommandSync(ce *CommandEvent) {
	user := ce.User
	create := len(ce.Args) > 0 && ce.Args[0] == "--create-all"

	user.log.Debugfln("syncing contacts and chats of user %s", user.MXID)
	ce.Reply("Updating contact and chat list...")
	err := user.Pulse.Sync()
	if err != nil {
		user.log.Errorln("Error syncing pulse:", err)
		ce.Reply("Failed to sync contact and chat list (see logs for details)")
		return
	}

	err = user.Pulse.SyncContacts()
	if err != nil {
		user.log.Errorln("Error syncing contacts pulse:", err)
		ce.Reply("Failed to sync contact (see logs for details)")
		return
	}

	// handler.log.Debugln("Importing contacts of", user.MXID)
	// _, err := user.Conn.Contacts()
	// if err != nil {
	// 	user.log.Errorln("Error updating contacts:", err)
	// 	ce.Reply("Failed to sync contact list (see logs for details)")
	// 	return
	// }
	// handler.log.Debugln("Importing chats of", user.MXID)
	// _, err = user.Conn.Chats()
	// if err != nil {
	// 	user.log.Errorln("Error updating chats:", err)
	// 	ce.Reply("Failed to sync chat list (see logs for details)")
	// 	return
	// }

	ce.Reply("Syncing contacts...")
	user.syncPuppets(nil)
	ce.Reply("Syncing chats...")
	user.syncPortals(nil, create)

	ce.Reply("Sync complete.")
}

const cmdDeletePortalHelp = `delete-portal - Delete the current portal. If the portal is used by other people, this is limited to bridge admins.`

func (handler *CommandHandler) CommandDeletePortal(ce *CommandEvent) {
	if ce.Portal == nil {
		ce.Reply("You must be in a portal room to use that command")
		return
	}

	if !ce.User.Admin {
		users := ce.Portal.GetUserIDs()
		if len(users) > 1 || (len(users) == 1 && users[0] != ce.User.MXID) {
			ce.Reply("Only bridge admins can delete portals with other Matrix users")
			return
		}
	}

	ce.Portal.log.Infoln(ce.User.MXID, "requested deletion of portal.")
	ce.Portal.Delete()
	ce.Portal.Cleanup(false)
}

const cmdDeleteAllPortalsHelp = `delete-all-portals - Delete all your portals that aren't used by any other user.'`

func (handler *CommandHandler) CommandDeleteAllPortals(ce *CommandEvent) {
	portals := ce.User.GetPortals()
	portalsToDelete := make([]*Portal, 0, len(portals))
	for _, portal := range portals {
		users := portal.GetUserIDs()
		if len(users) == 1 && users[0] == ce.User.MXID {
			portalsToDelete = append(portalsToDelete, portal)
		}
	}
	leave := func(portal *Portal) {
		if len(portal.MXID) > 0 {
			_, _ = portal.MainIntent().KickUser(portal.MXID, &mautrix.ReqKickUser{
				Reason: "Deleting portal",
				UserID: ce.User.MXID,
			})
		}
	}
	customPuppet := handler.bridge.GetPuppetByCustomMXID(ce.User.MXID)
	if customPuppet != nil && customPuppet.CustomIntent() != nil {
		intent := customPuppet.CustomIntent()
		leave = func(portal *Portal) {
			if len(portal.MXID) > 0 {
				_, _ = intent.LeaveRoom(portal.MXID)
				_, _ = intent.ForgetRoom(portal.MXID)
			}
		}
	}
	ce.Reply("Found %d portals with no other users, deleting...", len(portalsToDelete))
	for _, portal := range portalsToDelete {
		portal.Delete()
		leave(portal)
	}
	ce.Reply("Finished deleting portal info. Now cleaning up rooms in background. " +
		"You may already continue using the bridge. Use `sync` to recreate portals.")

	go func() {
		for _, portal := range portalsToDelete {
			portal.Cleanup(false)
		}
		ce.Reply("Finished background cleanup of deleted portal rooms.")
	}()
}

const cmdListHelp = `list <contacts|groups> [page] [items per page] - Get a list of all contacts and groups.`

const cmdSearchHelp = `search <contacts|groups> <search>`

func formatContacts(contacts bool, input map[string]pulsesms.Contact, filter string, bridge *Bridge) (result []string) {
	reg, err := regexp.Compile("[^a-zA-Z0-9]+")
	for _, contact := range input {
		if strings.Contains(strings.ToLower(contact.Name), filter) {
			if contacts {
				displayName, _ := bridge.Config.Bridge.FormatDisplayname(contact)
				cleanID := contact.PhoneNumber
				if err == nil {
					cleanID = reg.ReplaceAllString(cleanID, "")
				}
				cID := bridge.Config.Bridge.FormatUsername(cleanID)
				contactId := fmt.Sprintf("[${%s}](https://matrix.to/#/@%s:%s)", //TODO : change by room id
					contact.Name,
					cID,
					bridge.Config.Homeserver.Domain)
				result = append(result, fmt.Sprintf("* %s - `%s` %s",
					displayName,
					contact.PhoneNumber[:len(contact.PhoneNumber)],
					contactId))
			} else {
				result = append(result, fmt.Sprintf("* %s - `%s`", contact.Name, contact.PhoneNumber))
			}
		}
	}
	sort.Sort(sort.StringSlice(result))
	return
}

func (handler *CommandHandler) CommandSearch(ce *CommandEvent) {
	if len(ce.Args) < 2 {
		ce.Reply("**Usage:** `search <contacts|groups> <search, even multiple words>`")
		return
	}
	mode := strings.ToLower(ce.Args[0])
	if mode[0] != 'g' && mode[0] != 'c' {
		ce.Reply("**Usage:** `search <contacts|groups> <search, even multiple words>`")
		return
	}
	search := strings.ToLower(ce.Args[1])
	i := 2
	for i <= len(ce.Args)-1 {
		search += " " + strings.ToLower(ce.Args[i])
		i++
	}
	contacts := mode[0] == 'c'
	typeName := "Groups"
	if contacts {
		typeName = "Contacts"
	}

	result := formatContacts(contacts, ce.User.Pulse.Store.Contacts, search, handler.bridge)
	if len(result) == 0 {
		ce.Reply("No %s found", strings.ToLower(typeName))
		return
	}

	ce.Reply("### %s \n\n%s", typeName, strings.Join(result, "\n"))
}

func (handler *CommandHandler) CommandList(ce *CommandEvent) {
	if len(ce.Args) == 0 {
		ce.Reply("**Usage:** `list <contacts|groups> [page] [items per page]`")
		return
	}
	mode := strings.ToLower(ce.Args[0])
	if mode[0] != 'g' && mode[0] != 'c' {
		ce.Reply("**Usage:** `list <contacts|groups> [page] [items per page]`")
		return
	}

	var err error
	page := 1
	max := 100
	if len(ce.Args) > 1 {
		page, err = strconv.Atoi(ce.Args[1])
		if err != nil || page <= 0 {
			ce.Reply("\"%s\" isn't a valid page number", ce.Args[1])
			return
		}
	}
	if len(ce.Args) > 2 {
		max, err = strconv.Atoi(ce.Args[2])
		if err != nil || max <= 0 {
			ce.Reply("\"%s\" isn't a valid number of items per page", ce.Args[2])
			return
		} else if max > 400 {
			ce.Reply("Warning: a high number of items per page may fail to send a reply")
		}
	}
	contacts := mode[0] == 'c'
	typeName := "Groups"
	if contacts {
		typeName = "Contacts"
	}
	result := formatContacts(contacts, ce.User.Pulse.Store.Contacts, "", handler.bridge)
	if len(result) == 0 {
		ce.Reply("No %s found", strings.ToLower(typeName))
		return
	}
	pages := int(math.Ceil(float64(len(result)) / float64(max)))
	if (page-1)*max >= len(result) {
		if pages == 1 {
			ce.Reply("There is only 1 page of %s", strings.ToLower(typeName))
		} else {
			ce.Reply("There are only %d pages of %s", pages, strings.ToLower(typeName))
		}
		return
	}
	lastIndex := page * max
	if lastIndex > len(result) {
		lastIndex = len(result)
	}
	result = result[(page-1)*max : lastIndex]
	ce.Reply("### %s (page %d of %d)\n\n%s", typeName, page, pages, strings.Join(result, "\n"))
}

const cmdOpenHelp = `open <_group PID_> - Open a group chat portal.`

func (handler *CommandHandler) CommandOpen(ce *CommandEvent) {
	if len(ce.Args) == 0 {
		ce.Reply("**Usage:** `open <group PID>`")
		return
	}

	ce.Reply("not implemented")
	return

	// user := ce.User
	// pid := ce.Args[0]

	// if strings.HasSuffix(pid, whatsapp.NewUserSuffix) {
	// 	ce.Reply("That looks like a user PID. Did you mean `pm %s`?", pid[:len(pid)-len(whatsapp.NewUserSuffix)])
	// 	return
	// }

	// contact, ok := user.Pulse.Store.Contacts[pid]
	// if !ok {
	// 	ce.Reply("Group PID not found in contacts. Try syncing contacts with `sync` first.")
	// 	return
	// }

	// handler.log.Debugln("Importing", pid, "for", user)
	// portal := user.bridge.GetPortalByPID(database.GroupPortalKey(pid))
	// if len(portal.MXID) > 0 {
	// 	portal.Sync(user, contact)
	// 	ce.Reply("Portal room synced.")
	// } else {
	// 	portal.Sync(user, contact)
	// 	ce.Reply("Portal room created.")
	// }
	// _, _ = portal.MainIntent().InviteUser(portal.MXID, &mautrix.ReqInviteUser{UserID: user.MXID})
}

const cmdPMHelp = `pm [--force] <_international phone number_> - Open a private chat with the given phone number.`

func (handler *CommandHandler) CommandPM(ce *CommandEvent) {
	if len(ce.Args) == 0 {
		ce.Reply("**Usage:** `pm [--force] <international phone number>`")
		return
	}

	ce.Reply("not implemeneted")
	return

	// force := ce.Args[0] == "--force"
	// if force {
	// 	ce.Args = ce.Args[1:]
	// }

	// user := ce.User

	// number := strings.Join(ce.Args, "")
	// if number[0] == '+' {
	// 	number = number[1:]
	// }
	// for _, char := range number {
	// 	if char < '0' || char > '9' {
	// 		ce.Reply("Invalid phone number.")
	// 		return
	// 	}
	// }

	// pid := number

	// handler.log.Debugln("Importing", pid, "for", user)

	// contact, ok := user.Pulse.Store.Contacts[pid]
	// if !ok {
	// 	if !force {
	// 		ce.Reply("Phone number not found in contacts. Try syncing contacts with `sync` first. " +
	// 			"To create a portal anyway, use `pm --force <number>`.")
	// 		return
	// 	}
	// 	contact = pulsesms.Contact{PID: pid}
	// }
	// puppet := user.bridge.GetPuppetByPID(contact.PID)
	// puppet.Sync(user, contact)
	// portal := user.bridge.GetPortalByPID(database.NewPortalKey(contact.PID, user.PID))
	// if len(portal.MXID) > 0 {
	// 	var err error
	// 	if !user.IsRelaybot {
	// 		err = portal.MainIntent().EnsureInvited(portal.MXID, user.MXID)
	// 	}
	// 	if err != nil {
	// 		portal.log.Warnfln("Failed to invite %s to portal: %v. Creating new portal", user.MXID, err)
	// 		portal.MXID = ""
	// 	} else {
	// 		ce.Reply("You already have a private chat portal with that user at [%s](https://matrix.to/#/%s)", puppet.Displayname, portal.MXID)
	// 		return
	// 	}
	// }
	// err := portal.CreateMatrixRoom(user)
	// if err != nil {
	// 	ce.Reply("Failed to create portal room: %v", err)
	// 	return
	// }
	// ce.Reply("Created portal room and invited you to it.")
}

const cmdLoginMatrixHelp = `login-matrix <_access token_> - Replace your PulseSMS account's Matrix puppet with your real Matrix account.'`

func (handler *CommandHandler) CommandLoginMatrix(ce *CommandEvent) {
	if len(ce.Args) == 0 {
		ce.Reply("**Usage:** `login-matrix <access token>`")
		return
	}
	puppet := handler.bridge.GetPuppetByPID(ce.User.PID)
	err := puppet.SwitchCustomMXID(ce.Args[0], ce.User.MXID)
	if err != nil {
		ce.Reply("Failed to switch puppet: %v", err)
		return
	}
	ce.Reply("Successfully switched puppet")
}

const cmdLogoutMatrixHelp = `logout-matrix - Switch your PulseSMS account's Matrix puppet back to the default one.`

func (handler *CommandHandler) CommandLogoutMatrix(ce *CommandEvent) {
	puppet := handler.bridge.GetPuppetByPID(ce.User.PID)
	if len(puppet.CustomMXID) == 0 {
		ce.Reply("You had not changed your PulseSMS account's Matrix puppet.")
		return
	}
	err := puppet.SwitchCustomMXID("", "")
	if err != nil {
		ce.Reply("Failed to remove custom puppet: %v", err)
		return
	}
	ce.Reply("Successfully removed custom puppet")
}
