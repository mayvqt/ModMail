package modmail

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"sort"
	"strings"
	"time"

	"example.com/ModMail/internal/config"
	"example.com/ModMail/internal/storage"
	"github.com/bwmarrin/discordgo"
)

const (
	closeCommandName       = "close"
	blockCommandName       = "block"
	claimCommandName       = "claim"
	noteCommandName        = "note"
	priorityCommandName    = "priority"
	renameCommandName      = "rename"
	reopenCommandName      = "reopen"
	ticketCommandName      = "ticket"
	unblockCommandName     = "unblock"
	unclaimCommandName     = "unclaim"
	maxMessageLength       = 2000
	transcriptMessageLimit = 500
	transcriptMessageBatch = 100
	maxChannelNameLength   = 90
	maxCloseReasonLength   = 1000
	maxInternalNoteLength  = 1500
)

var noMentions = &discordgo.MessageAllowedMentions{}

type Service struct {
	cfg   config.Config
	store *storage.Store
	botID string
	log   *slog.Logger
}

func New(cfg config.Config, store *storage.Store) *Service {
	return &Service{cfg: cfg, store: store, log: slog.Default()}
}

func (s *Service) RegisterHandlers(dg *discordgo.Session) {
	dg.AddHandler(s.onReady)
	dg.AddHandler(s.onMessageCreate)
	dg.AddHandler(s.onInteractionCreate)
}

func (s *Service) onReady(dg *discordgo.Session, r *discordgo.Ready) {
	s.botID = r.User.ID
	s.log.Info("discord session ready", "bot_id", r.User.ID, "bot", userTag(r.User))
	s.validateDiscordSetup(dg)
	s.resumeScheduledDeletions(dg)
	if s.cfg.EnableSlash {
		s.registerCommands(dg, r.User.ID)
	}
}

func (s *Service) registerCommands(dg *discordgo.Session, appID string) {
	dmPermission := false
	commands := []*discordgo.ApplicationCommand{
		{
			Name:         blockCommandName,
			Description:  "Block a user from opening modmail tickets",
			DMPermission: &dmPermission,
			Options: []*discordgo.ApplicationCommandOption{
				{Type: discordgo.ApplicationCommandOptionUser, Name: "user", Description: "User to block", Required: true},
				{Type: discordgo.ApplicationCommandOptionString, Name: "reason", Description: "Block reason", Required: false, MaxLength: maxCloseReasonLength},
			},
		},
		{Name: claimCommandName, Description: "Claim the current modmail ticket", DMPermission: &dmPermission},
		{
			Name:         closeCommandName,
			Description:  "Close the current modmail ticket",
			DMPermission: &dmPermission,
			Options: []*discordgo.ApplicationCommandOption{{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "reason",
				Description: "Optional reason shown to the user and saved in logs",
				Required:    false,
				MaxLength:   maxCloseReasonLength,
			}},
		},
		{
			Name:         noteCommandName,
			Description:  "Add an internal staff note to this ticket",
			DMPermission: &dmPermission,
			Options: []*discordgo.ApplicationCommandOption{{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "text",
				Description: "Note text",
				Required:    true,
				MaxLength:   maxInternalNoteLength,
			}},
		},
		{
			Name:         priorityCommandName,
			Description:  "Set the current ticket priority",
			DMPermission: &dmPermission,
			Options: []*discordgo.ApplicationCommandOption{{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "level",
				Description: "Priority level",
				Required:    true,
				Choices: []*discordgo.ApplicationCommandOptionChoice{
					{Name: "low", Value: "low"},
					{Name: "normal", Value: "normal"},
					{Name: "high", Value: "high"},
					{Name: "urgent", Value: "urgent"},
				},
			}},
		},
		{
			Name:         renameCommandName,
			Description:  "Rename the current ticket channel",
			DMPermission: &dmPermission,
			Options: []*discordgo.ApplicationCommandOption{{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "name",
				Description: "New ticket channel name",
				Required:    true,
				MaxLength:   maxChannelNameLength,
			}},
		},
		{Name: reopenCommandName, Description: "Reopen the latest closed ticket in this channel", DMPermission: &dmPermission},
		{Name: ticketCommandName, Description: "Show details for the current modmail ticket", DMPermission: &dmPermission},
		{
			Name:         unblockCommandName,
			Description:  "Unblock a user from opening modmail tickets",
			DMPermission: &dmPermission,
			Options:      []*discordgo.ApplicationCommandOption{{Type: discordgo.ApplicationCommandOptionUser, Name: "user", Description: "User to unblock", Required: true}},
		},
		{Name: unclaimCommandName, Description: "Unclaim the current modmail ticket", DMPermission: &dmPermission},
	}
	if _, err := dg.ApplicationCommandBulkOverwrite(appID, s.cfg.GuildID, commands); err != nil {
		s.log.Error("register slash commands", "error", err)
	}
}

func (s *Service) onInteractionCreate(dg *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}
	if i.GuildID != s.cfg.GuildID || !s.isInStaffCategory(dg, i.ChannelID) {
		s.respondInteraction(dg, i, "This command only works in modmail ticket channels.")
		return
	}
	if !s.deferInteraction(dg, i) {
		return
	}

	switch i.ApplicationCommandData().Name {
	case blockCommandName:
		m := interactionMessage(i)
		if err := s.handleBlock(dg, m, slashUserOption(i, "user"), slashStringOption(i, "reason")); err != nil {
			s.editInteraction(dg, i, "Could not block that user.")
			return
		}
		s.editInteraction(dg, i, "User blocked.")
	case claimCommandName:
		m := interactionMessage(i)
		if err := s.handleClaim(dg, m); err != nil {
			s.editInteraction(dg, i, "Could not claim this ticket.")
			return
		}
		s.editInteraction(dg, i, "Ticket claimed.")
	case closeCommandName:
		m := interactionMessage(i)
		if err := s.handleClose(dg, m, slashStringOption(i, "reason")); err != nil {
			s.editInteraction(dg, i, "Could not close this ticket.")
			return
		}
		s.editInteraction(dg, i, "Ticket closed.")
	case noteCommandName:
		m := interactionMessage(i)
		if err := s.handleNote(dg, m, slashStringOption(i, "text")); err != nil {
			s.editInteraction(dg, i, "Could not add note.")
			return
		}
		s.editInteraction(dg, i, "Note added.")
	case priorityCommandName:
		if err := s.handlePriority(dg, i.ChannelID, slashStringOption(i, "level")); err != nil {
			s.editInteraction(dg, i, "Could not set ticket priority.")
			return
		}
		s.editInteraction(dg, i, "Ticket priority updated.")
	case renameCommandName:
		if err := s.handleRename(dg, i.ChannelID, slashStringOption(i, "name")); err != nil {
			s.editInteraction(dg, i, "Could not rename this ticket.")
			return
		}
		s.editInteraction(dg, i, "Ticket renamed.")
	case reopenCommandName:
		m := interactionMessage(i)
		if err := s.handleReopen(dg, m); err != nil {
			s.editInteraction(dg, i, "Could not reopen this ticket.")
			return
		}
		s.editInteraction(dg, i, "Ticket reopened.")
	case ticketCommandName:
		if err := s.handleTicketInfo(dg, i.ChannelID); err != nil {
			s.editInteraction(dg, i, "Could not find a ticket in this channel.")
			return
		}
		s.editInteraction(dg, i, "Ticket details posted.")
	case unblockCommandName:
		m := interactionMessage(i)
		if err := s.handleUnblock(dg, m, slashUserOption(i, "user")); err != nil {
			s.editInteraction(dg, i, "Could not unblock that user.")
			return
		}
		s.editInteraction(dg, i, "User unblocked.")
	case unclaimCommandName:
		if err := s.handleUnclaim(dg, i.ChannelID); err != nil {
			s.editInteraction(dg, i, "Could not unclaim this ticket.")
			return
		}
		s.editInteraction(dg, i, "Ticket unclaimed.")
	default:
		s.editInteraction(dg, i, "Unknown command.")
	}
}

func (s *Service) respondInteraction(dg *discordgo.Session, i *discordgo.InteractionCreate, msg string) {
	_ = dg.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content:         msg,
			Flags:           discordgo.MessageFlagsEphemeral,
			AllowedMentions: noMentions,
		},
	})
}

func (s *Service) deferInteraction(dg *discordgo.Session, i *discordgo.InteractionCreate) bool {
	err := dg.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags:           discordgo.MessageFlagsEphemeral,
			AllowedMentions: noMentions,
		},
	})
	if err != nil {
		s.log.Error("defer interaction", "error", err)
		return false
	}
	return true
}

func (s *Service) editInteraction(dg *discordgo.Session, i *discordgo.InteractionCreate, msg string) {
	_, err := dg.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content:         &msg,
		AllowedMentions: noMentions,
	})
	if err != nil {
		s.log.Error("edit interaction response", "error", err)
	}
}

func (s *Service) onMessageCreate(dg *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author == nil || m.Author.Bot {
		return
	}

	if m.GuildID == "" {
		if err := s.handleUserDM(dg, m); err != nil {
			s.log.Error("handle user dm", "user_id", m.Author.ID, "error", err)
			_ = s.sendMessageWithRetry(dg, m.ChannelID, "Sorry, something went wrong while opening your modmail ticket.")
		}
		return
	}

	if m.GuildID != s.cfg.GuildID || !s.isInStaffCategory(dg, m.ChannelID) {
		return
	}

	if cmd, ok := parseCommand(m.Content, s.cfg.CommandPrefix); ok {
		switch cmd.Name {
		case blockCommandName:
			userID, reason := splitFirstArg(cmd.Args)
			if err := s.handleBlock(dg, m, userID, reason); err != nil {
				s.log.Error("handle block", "channel_id", m.ChannelID, "error", err)
				_ = s.sendMessageWithRetry(dg, m.ChannelID, fmt.Sprintf("Usage: `%sblock <user_id_or_mention> [reason]`", s.cfg.CommandPrefix))
			}
		case claimCommandName:
			if err := s.handleClaim(dg, m); err != nil {
				s.log.Error("handle claim", "channel_id", m.ChannelID, "error", err)
				_ = s.sendMessageWithRetry(dg, m.ChannelID, "Could not claim this ticket.")
			}
		case closeCommandName:
			if err := s.handleClose(dg, m, cmd.Args); err != nil {
				s.log.Error("handle close", "channel_id", m.ChannelID, "error", err)
				_ = s.sendMessageWithRetry(dg, m.ChannelID, "Could not close this ticket.")
			}
		case noteCommandName:
			if err := s.handleNote(dg, m, cmd.Args); err != nil {
				s.log.Error("handle note", "channel_id", m.ChannelID, "error", err)
				_ = s.sendMessageWithRetry(dg, m.ChannelID, "Could not add note.")
			}
		case priorityCommandName:
			if err := s.handlePriority(dg, m.ChannelID, cmd.Args); err != nil {
				s.log.Error("handle priority", "channel_id", m.ChannelID, "error", err)
				_ = s.sendMessageWithRetry(dg, m.ChannelID, fmt.Sprintf("Usage: `%spriority low|normal|high|urgent`", s.cfg.CommandPrefix))
			}
		case renameCommandName:
			if err := s.handleRename(dg, m.ChannelID, cmd.Args); err != nil {
				s.log.Error("handle rename", "channel_id", m.ChannelID, "error", err)
				_ = s.sendMessageWithRetry(dg, m.ChannelID, "Could not rename this ticket.")
			}
		case reopenCommandName:
			if err := s.handleReopen(dg, m); err != nil {
				s.log.Error("handle reopen", "channel_id", m.ChannelID, "error", err)
				_ = s.sendMessageWithRetry(dg, m.ChannelID, "Could not reopen this ticket.")
			}
		case ticketCommandName:
			if err := s.handleTicketInfo(dg, m.ChannelID); err != nil {
				s.log.Error("handle ticket info", "channel_id", m.ChannelID, "error", err)
				_ = s.sendMessageWithRetry(dg, m.ChannelID, "Could not find a ticket in this channel.")
			}
		case unblockCommandName:
			userID, _ := splitFirstArg(cmd.Args)
			if err := s.handleUnblock(dg, m, userID); err != nil {
				s.log.Error("handle unblock", "channel_id", m.ChannelID, "error", err)
				_ = s.sendMessageWithRetry(dg, m.ChannelID, fmt.Sprintf("Usage: `%sunblock <user_id_or_mention>`", s.cfg.CommandPrefix))
			}
		case unclaimCommandName:
			if err := s.handleUnclaim(dg, m.ChannelID); err != nil {
				s.log.Error("handle unclaim", "channel_id", m.ChannelID, "error", err)
				_ = s.sendMessageWithRetry(dg, m.ChannelID, "Could not unclaim this ticket.")
			}
		default:
			_ = s.sendMessageWithRetry(dg, m.ChannelID, fmt.Sprintf("Unknown command. Use `%sclose`, `%sreopen`, `%snote`, `%sticket`, `%sclaim`, `%spriority`, or `%sblock`.", s.cfg.CommandPrefix, s.cfg.CommandPrefix, s.cfg.CommandPrefix, s.cfg.CommandPrefix, s.cfg.CommandPrefix, s.cfg.CommandPrefix, s.cfg.CommandPrefix))
		}
		return
	}

	if err := s.handleStaffReply(dg, m); err != nil && !errors.Is(err, sql.ErrNoRows) {
		s.log.Error("handle staff reply", "channel_id", m.ChannelID, "error", err)
	}
}

func (s *Service) isInStaffCategory(dg *discordgo.Session, channelID string) bool {
	ch, err := dg.State.Channel(channelID)
	if err != nil || ch == nil {
		ch, err = dg.Channel(channelID)
		if err != nil || ch == nil {
			return false
		}
	}
	return ch.ParentID == s.cfg.StaffCategoryID
}

func (s *Service) validateDiscordSetup(dg *discordgo.Session) {
	if _, err := dg.Guild(s.cfg.GuildID); err != nil {
		s.log.Error("validate guild", "guild_id", s.cfg.GuildID, "error", err)
	}
	category, err := dg.Channel(s.cfg.StaffCategoryID)
	if err != nil {
		s.log.Error("validate staff category", "category_id", s.cfg.StaffCategoryID, "error", err)
	} else if category.Type != discordgo.ChannelTypeGuildCategory {
		s.log.Warn("staff category id does not point to a category", "category_id", s.cfg.StaffCategoryID, "type", category.Type)
	}
	if s.cfg.LogChannelID != "" {
		if _, err := dg.Channel(s.cfg.LogChannelID); err != nil {
			s.log.Error("validate log channel", "channel_id", s.cfg.LogChannelID, "error", err)
		}
	}
	if s.cfg.StaffRoleID != "" {
		if _, err := dg.State.Role(s.cfg.GuildID, s.cfg.StaffRoleID); err != nil {
			roles, roleErr := dg.GuildRoles(s.cfg.GuildID)
			if roleErr != nil {
				s.log.Error("validate staff role", "role_id", s.cfg.StaffRoleID, "error", roleErr)
			} else if !roleExists(roles, s.cfg.StaffRoleID) {
				s.log.Warn("staff role not found", "role_id", s.cfg.StaffRoleID)
			}
		}
	}
	if s.botID != "" {
		perms, err := dg.UserChannelPermissions(s.botID, s.cfg.StaffCategoryID)
		if err != nil {
			s.log.Warn("validate bot permissions", "category_id", s.cfg.StaffCategoryID, "error", err)
		} else {
			required := int64(discordgo.PermissionViewChannel | discordgo.PermissionSendMessages | discordgo.PermissionManageChannels | discordgo.PermissionReadMessageHistory)
			if perms&required != required {
				s.log.Warn("bot may be missing required category permissions", "category_id", s.cfg.StaffCategoryID, "permissions", perms, "required", required)
			}
		}
	}
}

func roleExists(roles []*discordgo.Role, roleID string) bool {
	for _, role := range roles {
		if role.ID == roleID {
			return true
		}
	}
	return false
}

func (s *Service) handleUserDM(dg *discordgo.Session, m *discordgo.MessageCreate) error {
	if !hasRelayContent(m) {
		return s.sendMessageWithRetry(dg, m.ChannelID, "Please include a message or attachment to open a modmail ticket.")
	}
	if block, err := s.store.GetBlockedUser(m.Author.ID); err == nil {
		s.log.Info("blocked user attempted modmail", "user_id", m.Author.ID, "reason", block.Reason)
		return s.sendMessageWithRetry(dg, m.ChannelID, "You are currently blocked from opening modmail tickets.")
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	ticket, err := s.store.GetOpenTicketByUser(m.Author.ID)
	if errors.Is(err, sql.ErrNoRows) {
		ch, createErr := s.createTicketChannel(dg, m.Author)
		if createErr != nil {
			return createErr
		}
		ticket, err = s.store.CreateTicket(m.Author.ID, ch.ID)
		if errors.Is(err, storage.ErrOpenTicketExists) {
			_, _ = dg.ChannelDelete(ch.ID)
			ticket, err = s.store.GetOpenTicketByUser(m.Author.ID)
		}
		if err != nil {
			return err
		}
		s.log.Info("ticket created", "ticket_id", ticket.ID, "user_id", ticket.UserID, "channel_id", ticket.ChannelID)

		_ = s.sendMessageWithRetry(dg, m.ChannelID, "Your message has been sent to the staff team. Reply here to add more information.")
		_ = s.sendMessageWithRetry(dg, ch.ID, fmt.Sprintf("New modmail ticket from **%s** (`%s`). Use `%sclose [reason]` or `/close` to close.", userTag(m.Author), m.Author.ID, s.cfg.CommandPrefix))
	} else if err != nil {
		return err
	}

	return s.relayToStaff(dg, ticket.ChannelID, m)
}

func (s *Service) createTicketChannel(dg *discordgo.Session, user *discordgo.User) (*discordgo.Channel, error) {
	name := sanitizeChannelName("modmail-" + user.Username)
	overwrites := []*discordgo.PermissionOverwrite{{
		ID:   s.cfg.GuildID,
		Type: discordgo.PermissionOverwriteTypeRole,
		Deny: discordgo.PermissionViewChannel,
	}}
	if s.cfg.StaffRoleID != "" {
		overwrites = append(overwrites, &discordgo.PermissionOverwrite{
			ID:    s.cfg.StaffRoleID,
			Type:  discordgo.PermissionOverwriteTypeRole,
			Allow: discordgo.PermissionViewChannel | discordgo.PermissionSendMessages | discordgo.PermissionReadMessageHistory,
		})
	}
	if s.botID != "" {
		overwrites = append(overwrites, &discordgo.PermissionOverwrite{
			ID:    s.botID,
			Type:  discordgo.PermissionOverwriteTypeMember,
			Allow: discordgo.PermissionViewChannel | discordgo.PermissionSendMessages | discordgo.PermissionManageChannels | discordgo.PermissionReadMessageHistory,
		})
	}

	return dg.GuildChannelCreateComplex(s.cfg.GuildID, discordgo.GuildChannelCreateData{
		Name:                 name,
		Type:                 discordgo.ChannelTypeGuildText,
		ParentID:             s.cfg.StaffCategoryID,
		Topic:                fmt.Sprintf("Modmail ticket for %s (%s)", userTag(user), user.ID),
		PermissionOverwrites: overwrites,
	})
}

func (s *Service) relayToStaff(dg *discordgo.Session, channelID string, m *discordgo.MessageCreate) error {
	msg := relayMessage(fmt.Sprintf("**%s:**", userTag(m.Author)), m.Content, m.Attachments, m.StickerItems)
	if msg == "" {
		return nil
	}
	return s.sendMessageWithRetry(dg, channelID, msg)
}

func (s *Service) handleStaffReply(dg *discordgo.Session, m *discordgo.MessageCreate) error {
	ticket, err := s.store.GetOpenTicketByChannel(m.ChannelID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return s.handleReplyWithoutOpenTicket(dg, m.ChannelID)
		}
		return err
	}

	msg := relayMessage(fmt.Sprintf("**%s:**", s.staffReplyLabel(m.Author)), m.Content, m.Attachments, m.StickerItems)
	if msg == "" {
		return nil
	}

	userCh, err := dg.UserChannelCreate(ticket.UserID)
	if err != nil {
		return err
	}
	return s.sendMessageWithRetry(dg, userCh.ID, msg)
}

func (s *Service) handleReplyWithoutOpenTicket(dg *discordgo.Session, channelID string) error {
	ticket, err := s.store.GetLatestTicketByChannel(channelID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if ticket.Status == storage.TicketStatusClosed {
		return s.sendMessageWithRetry(dg, channelID, fmt.Sprintf("This ticket is closed. Use `%sreopen` or `/reopen` before replying.", s.cfg.CommandPrefix))
	}
	return nil
}

func (s *Service) handleClose(dg *discordgo.Session, m *discordgo.MessageCreate, reason string) error {
	ticket, err := s.store.GetOpenTicketByChannel(m.ChannelID)
	if err != nil {
		return err
	}
	reason = strings.TrimSpace(reason)
	reason = truncateText(reason, maxCloseReasonLength)

	transcript, transcriptErr := s.buildTranscript(dg, m.ChannelID)
	if transcriptErr != nil {
		s.log.Error("build transcript", "channel_id", m.ChannelID, "error", transcriptErr)
	}

	if err := s.store.CloseTicket(m.ChannelID, reason); err != nil {
		return err
	}
	s.log.Info("ticket closed", "ticket_id", ticket.ID, "channel_id", m.ChannelID, "user_id", ticket.UserID, "closed_by", userID(m.Author), "reason", reason)

	if userCh, err := dg.UserChannelCreate(ticket.UserID); err == nil {
		_ = s.sendMessageWithRetry(dg, userCh.ID, closeNotice("Your modmail ticket has been closed.", reason)+"\nSend another message here to open a new ticket.")
	}

	_ = s.sendMessageWithRetry(dg, m.ChannelID, closeNotice("Ticket closed.", reason))
	if s.cfg.LogChannelID != "" {
		logMsg := closeNotice(fmt.Sprintf("Ticket `%d` for user `%s` was closed by %s.", ticket.ID, ticket.UserID, userTag(m.Author)), reason)
		_ = s.sendMessageWithRetry(dg, s.cfg.LogChannelID, logMsg)
		if len(transcript) > 0 {
			_, _ = dg.ChannelFileSend(s.cfg.LogChannelID, fmt.Sprintf("ticket-%d-transcript.html", ticket.ID), bytes.NewReader(transcript))
		}
	}
	if s.cfg.AutoDeleteAfter > 0 {
		_ = s.sendMessageWithRetry(dg, m.ChannelID, fmt.Sprintf("This channel will be deleted in %s.", s.cfg.AutoDeleteAfter))
		s.scheduleClosedTicketDeletion(dg, m.ChannelID, s.cfg.AutoDeleteAfter)
	}

	return nil
}

func (s *Service) scheduleClosedTicketDeletion(dg *discordgo.Session, channelID string, wait time.Duration) {
	dueAt := time.Now().Add(wait).UTC()
	if err := s.store.ScheduleDeletion(channelID, dueAt); err != nil {
		s.log.Error("schedule ticket deletion", "channel_id", channelID, "error", err)
		return
	}
	go func() {
		time.Sleep(time.Until(dueAt))
		s.deleteClosedTicketIfDue(dg, channelID)
	}()
}

func (s *Service) resumeScheduledDeletions(dg *discordgo.Session) {
	deletions, err := s.store.ListScheduledDeletions()
	if err != nil {
		s.log.Error("list scheduled deletions", "error", err)
		return
	}
	for _, deletion := range deletions {
		channelID := deletion.ChannelID
		dueAt := deletion.DueAt
		go func() {
			time.Sleep(maxDuration(0, time.Until(dueAt)))
			s.deleteClosedTicketIfDue(dg, channelID)
		}()
	}
	if len(deletions) > 0 {
		s.log.Info("resumed scheduled deletions", "count", len(deletions))
	}
}

func (s *Service) deleteClosedTicketIfDue(dg *discordgo.Session, channelID string) {
	deletion, err := s.store.GetScheduledDeletion(channelID)
	if errors.Is(err, sql.ErrNoRows) {
		return
	}
	if err != nil {
		s.log.Error("scheduled deletion lookup", "channel_id", channelID, "error", err)
		return
	}
	if time.Now().Before(deletion.DueAt) {
		return
	}
	ticket, err := s.store.GetLatestTicketByChannel(channelID)
	if err != nil {
		s.log.Error("auto-delete ticket lookup", "channel_id", channelID, "error", err)
		return
	}
	if ticket.Status != storage.TicketStatusClosed {
		return
	}
	if _, err := dg.ChannelDelete(channelID); err != nil {
		s.log.Error("auto-delete channel", "channel_id", channelID, "error", err)
		return
	}
	if err := s.store.DeleteScheduledDeletion(channelID); err != nil {
		s.log.Error("delete scheduled deletion", "channel_id", channelID, "error", err)
	}
	s.log.Info("ticket channel auto-deleted", "ticket_id", ticket.ID, "channel_id", channelID)
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

func (s *Service) handleBlock(dg *discordgo.Session, m *discordgo.MessageCreate, targetUser, reason string) error {
	targetUser = normalizeUserID(targetUser)
	if targetUser == "" {
		return errors.New("target user is required")
	}
	reason = truncateText(strings.TrimSpace(reason), maxCloseReasonLength)
	if err := s.store.BlockUser(targetUser, reason, userID(m.Author)); err != nil {
		return err
	}
	s.log.Info("user blocked", "user_id", targetUser, "blocked_by", userID(m.Author), "reason", reason)
	return s.sendMessageWithRetry(dg, m.ChannelID, fmt.Sprintf("User `%s` blocked from opening modmail tickets.", targetUser))
}

func (s *Service) handleUnblock(dg *discordgo.Session, m *discordgo.MessageCreate, targetUser string) error {
	targetUser = normalizeUserID(targetUser)
	if targetUser == "" {
		return errors.New("target user is required")
	}
	if err := s.store.UnblockUser(targetUser); err != nil {
		return err
	}
	s.log.Info("user unblocked", "user_id", targetUser, "unblocked_by", userID(m.Author))
	return s.sendMessageWithRetry(dg, m.ChannelID, fmt.Sprintf("User `%s` unblocked.", targetUser))
}

func (s *Service) handleClaim(dg *discordgo.Session, m *discordgo.MessageCreate) error {
	ticket, err := s.store.GetLatestTicketByChannel(m.ChannelID)
	if err != nil {
		return err
	}
	if err := s.store.ClaimTicket(m.ChannelID, userID(m.Author), userTag(m.Author)); err != nil {
		return err
	}
	s.log.Info("ticket claimed", "ticket_id", ticket.ID, "channel_id", m.ChannelID, "claimed_by", userID(m.Author))
	return s.sendMessageWithRetry(dg, m.ChannelID, fmt.Sprintf("Ticket claimed by %s.", userTag(m.Author)))
}

func (s *Service) handleUnclaim(dg *discordgo.Session, channelID string) error {
	ticket, err := s.store.GetLatestTicketByChannel(channelID)
	if err != nil {
		return err
	}
	if err := s.store.UnclaimTicket(channelID); err != nil {
		return err
	}
	s.log.Info("ticket unclaimed", "ticket_id", ticket.ID, "channel_id", channelID)
	return s.sendMessageWithRetry(dg, channelID, "Ticket unclaimed.")
}

func (s *Service) handlePriority(dg *discordgo.Session, channelID, priority string) error {
	priority = strings.ToLower(strings.TrimSpace(priority))
	if !validPriority(priority) {
		return errors.New("invalid priority")
	}
	ticket, err := s.store.GetLatestTicketByChannel(channelID)
	if err != nil {
		return err
	}
	if err := s.store.SetTicketPriority(channelID, priority); err != nil {
		return err
	}
	s.log.Info("ticket priority updated", "ticket_id", ticket.ID, "channel_id", channelID, "priority", priority)
	return s.sendMessageWithRetry(dg, channelID, fmt.Sprintf("Ticket priority set to `%s`.", priority))
}

func (s *Service) handleRename(dg *discordgo.Session, channelID, name string) error {
	name = sanitizeChannelName(name)
	if name == "" {
		return errors.New("channel name is required")
	}
	if _, err := dg.ChannelEdit(channelID, &discordgo.ChannelEdit{Name: name}); err != nil {
		return err
	}
	s.log.Info("ticket channel renamed", "channel_id", channelID, "name", name)
	return nil
}

func (s *Service) handleNote(dg *discordgo.Session, m *discordgo.MessageCreate, note string) error {
	ticket, err := s.store.GetLatestTicketByChannel(m.ChannelID)
	if err != nil {
		return err
	}
	note = strings.TrimSpace(note)
	note = truncateText(note, maxInternalNoteLength)
	if note == "" {
		return s.sendMessageWithRetry(dg, m.ChannelID, fmt.Sprintf("Usage: `%s%s <staff-only note>`", s.cfg.CommandPrefix, noteCommandName))
	}

	stored, err := s.store.AddNote(ticket.ID, userID(m.Author), userTag(m.Author), note)
	if err != nil {
		return err
	}
	msg := fmt.Sprintf("**Internal note** by %s on ticket `%d`:\n%s", stored.AuthorTag, ticket.ID, stored.Body)
	if s.cfg.LogChannelID != "" {
		_ = s.sendMessageWithRetry(dg, s.cfg.LogChannelID, msg)
	}
	s.log.Info("ticket note added", "ticket_id", ticket.ID, "note_id", stored.ID, "author_id", stored.AuthorID)
	return s.sendMessageWithRetry(dg, m.ChannelID, msg)
}

func (s *Service) handleReopen(dg *discordgo.Session, m *discordgo.MessageCreate) error {
	ticket, err := s.store.GetLatestTicketByChannel(m.ChannelID)
	if err != nil {
		return err
	}
	if ticket.Status == storage.TicketStatusOpen {
		return s.sendMessageWithRetry(dg, m.ChannelID, "Ticket is already open.")
	}
	if err := s.store.ReopenTicket(m.ChannelID); err != nil {
		if errors.Is(err, storage.ErrOpenTicketExists) {
			return s.sendMessageWithRetry(dg, m.ChannelID, "Cannot reopen: this user already has another open ticket.")
		}
		return err
	}
	if err := s.store.DeleteScheduledDeletion(m.ChannelID); err != nil {
		s.log.Error("delete scheduled deletion after reopen", "channel_id", m.ChannelID, "error", err)
	}
	s.log.Info("ticket reopened", "ticket_id", ticket.ID, "channel_id", m.ChannelID, "user_id", ticket.UserID, "reopened_by", userID(m.Author))

	userCh, err := dg.UserChannelCreate(ticket.UserID)
	if err == nil {
		_ = s.sendMessageWithRetry(dg, userCh.ID, "Your modmail ticket has been reopened by staff.")
	}
	return s.sendMessageWithRetry(dg, m.ChannelID, "Ticket reopened.")
}

func (s *Service) handleTicketInfo(dg *discordgo.Session, channelID string) error {
	ticket, err := s.store.GetLatestTicketByChannel(channelID)
	if err != nil {
		return err
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Ticket `%d`\n", ticket.ID))
	b.WriteString(fmt.Sprintf("User: `%s`\n", ticket.UserID))
	b.WriteString(fmt.Sprintf("Status: `%s`\n", ticket.Status))
	b.WriteString(fmt.Sprintf("Priority: `%s`\n", ticket.Priority))
	if ticket.ClaimedTag.Valid {
		b.WriteString(fmt.Sprintf("Claimed by: %s\n", ticket.ClaimedTag.String))
	}
	b.WriteString(fmt.Sprintf("Opened: `%s`", ticket.CreatedAt.Format(time.RFC3339)))
	if ticket.ClosedAt.Valid {
		b.WriteString(fmt.Sprintf("\nClosed: `%s`", ticket.ClosedAt.Time.Format(time.RFC3339)))
	}
	if ticket.ClosedReason.Valid && strings.TrimSpace(ticket.ClosedReason.String) != "" {
		b.WriteString("\nClose reason: ")
		b.WriteString(ticket.ClosedReason.String)
	}
	notes, err := s.store.ListNotes(ticket.ID, 5)
	if err != nil {
		return err
	}
	if len(notes) > 0 {
		b.WriteString("\n\nRecent notes:")
		for _, note := range notes {
			b.WriteString(fmt.Sprintf("\n- `%s` %s: %s", note.CreatedAt.Format(time.RFC3339), note.AuthorTag, truncateText(note.Body, 160)))
		}
	}

	return s.sendMessageWithRetry(dg, channelID, b.String())
}

func (s *Service) buildTranscript(dg *discordgo.Session, channelID string) ([]byte, error) {
	var all []*discordgo.Message
	before := ""
	for {
		batch, err := dg.ChannelMessages(channelID, transcriptMessageBatch, before, "", "")
		if err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			break
		}
		all = append(all, batch...)
		before = batch[len(batch)-1].ID
		if len(batch) < transcriptMessageBatch || len(all) >= transcriptMessageLimit {
			break
		}
	}
	if len(all) == 0 {
		return nil, nil
	}

	sort.Slice(all, func(i, j int) bool {
		return all[i].Timestamp.Before(all[j].Timestamp)
	})

	var b strings.Builder
	b.WriteString("<!doctype html><html><head><meta charset=\"utf-8\"><title>Modmail transcript</title>")
	b.WriteString("<style>body{font-family:system-ui,sans-serif;line-height:1.45;margin:24px;max-width:960px}.msg{border-bottom:1px solid #ddd;padding:12px 0}.meta{color:#555;font-size:13px}.content{white-space:pre-wrap;margin-top:6px}.attachment{display:block;margin-top:4px}</style>")
	b.WriteString("</head><body><h1>Modmail Transcript</h1>")
	for _, msg := range all {
		author := "unknown"
		if msg.Author != nil {
			author = userTag(msg.Author)
		}
		b.WriteString("<div class=\"msg\"><div class=\"meta\">")
		b.WriteString(html.EscapeString(msg.Timestamp.Format(time.RFC3339)))
		b.WriteString(" - ")
		b.WriteString(html.EscapeString(author))
		b.WriteString("</div>")
		if strings.TrimSpace(msg.Content) != "" {
			b.WriteString("<div class=\"content\">")
			b.WriteString(html.EscapeString(msg.Content))
			b.WriteString("</div>")
		}
		for _, a := range msg.Attachments {
			url := html.EscapeString(a.URL)
			b.WriteString("<a class=\"attachment\" href=\"")
			b.WriteString(url)
			b.WriteString("\">")
			b.WriteString(url)
			b.WriteString("</a>")
		}
		for _, sticker := range msg.StickerItems {
			b.WriteString("<div class=\"content\">[sticker: ")
			b.WriteString(html.EscapeString(sticker.Name))
			b.WriteString("]</div>")
		}
		b.WriteString("</div>")
	}
	b.WriteString("</body></html>")

	return []byte(b.String()), nil
}

func (s *Service) sendMessageWithRetry(dg *discordgo.Session, channelID, content string) error {
	for _, chunk := range splitDiscordMessage(content) {
		var lastErr error
		for i := 0; i < 3; i++ {
			_, err := dg.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
				Content:         chunk,
				AllowedMentions: noMentions,
			})
			if err == nil {
				lastErr = nil
				break
			}
			lastErr = err
			time.Sleep(time.Duration(i+1) * 250 * time.Millisecond)
		}
		if lastErr != nil {
			return lastErr
		}
	}
	return nil
}

func userTag(u *discordgo.User) string {
	if u == nil {
		return "unknown"
	}
	if u.Discriminator == "0" || u.Discriminator == "" {
		return "@" + u.Username
	}
	return u.Username + "#" + u.Discriminator
}

func sanitizeChannelName(s string) string {
	s = strings.ToLower(s)
	replacer := strings.NewReplacer(" ", "-", "_", "-", ".", "-", "#", "")
	s = replacer.Replace(s)
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "modmail-user"
	}
	if len(out) > maxChannelNameLength {
		out = out[:maxChannelNameLength]
	}
	return out
}

type prefixCommand struct {
	Name string
	Args string
}

func parseCommand(content, prefix string) (prefixCommand, bool) {
	content = strings.TrimSpace(content)
	if prefix == "" || !strings.HasPrefix(content, prefix) {
		return prefixCommand{}, false
	}
	content = strings.TrimSpace(strings.TrimPrefix(content, prefix))
	if content == "" {
		return prefixCommand{}, false
	}
	name, args, _ := strings.Cut(content, " ")
	return prefixCommand{Name: strings.ToLower(name), Args: strings.TrimSpace(args)}, true
}

func slashStringOption(i *discordgo.InteractionCreate, name string) string {
	for _, opt := range i.ApplicationCommandData().Options {
		if opt.Name == name && opt.Type == discordgo.ApplicationCommandOptionString {
			return strings.TrimSpace(opt.StringValue())
		}
	}
	return ""
}

func slashUserOption(i *discordgo.InteractionCreate, name string) string {
	for _, opt := range i.ApplicationCommandData().Options {
		if opt.Name == name && opt.Type == discordgo.ApplicationCommandOptionUser {
			return normalizeUserID(opt.UserValue(nil).ID)
		}
	}
	return ""
}

func interactionMessage(i *discordgo.InteractionCreate) *discordgo.MessageCreate {
	var author *discordgo.User
	if i.Member != nil {
		author = i.Member.User
	}
	if author == nil {
		author = i.User
	}
	return &discordgo.MessageCreate{Message: &discordgo.Message{ChannelID: i.ChannelID, Author: author}}
}

func hasRelayContent(m *discordgo.MessageCreate) bool {
	return strings.TrimSpace(m.Content) != "" || len(m.Attachments) > 0 || len(m.StickerItems) > 0
}

func relayMessage(header, content string, attachments []*discordgo.MessageAttachment, stickers []*discordgo.StickerItem) string {
	content = strings.TrimSpace(content)
	if content == "" && len(attachments) == 0 && len(stickers) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString(header)
	if content != "" {
		b.WriteString("\n")
		b.WriteString(content)
	}
	for _, a := range attachments {
		b.WriteString("\n")
		b.WriteString(a.URL)
	}
	for _, sticker := range stickers {
		b.WriteString("\n")
		b.WriteString("[sticker: ")
		b.WriteString(sticker.Name)
		b.WriteString("]")
	}
	return b.String()
}

func closeNotice(prefix, reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return prefix
	}
	return prefix + "\nReason: " + reason
}

func (s *Service) staffReplyLabel(u *discordgo.User) string {
	switch s.cfg.StaffIdentity {
	case "named":
		return "Staff " + userTag(u)
	case "role":
		return s.cfg.StaffReplyLabel
	default:
		return "Staff"
	}
}

func userID(u *discordgo.User) string {
	if u == nil {
		return ""
	}
	return u.ID
}

func normalizeUserID(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "<@")
	s = strings.TrimPrefix(s, "!")
	s = strings.TrimSuffix(s, ">")
	return strings.TrimSpace(s)
}

func splitFirstArg(s string) (string, string) {
	first, rest, _ := strings.Cut(strings.TrimSpace(s), " ")
	return first, strings.TrimSpace(rest)
}

func validPriority(priority string) bool {
	switch priority {
	case "low", "normal", "high", "urgent":
		return true
	default:
		return false
	}
}

func truncateText(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
}

func splitDiscordMessage(content string) []string {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil
	}

	var chunks []string
	for len([]rune(content)) > maxMessageLength {
		window := string([]rune(content)[:maxMessageLength])
		splitAt := strings.LastIndex(window, "\n")
		if splitAt < 1 {
			splitAt = strings.LastIndex(window, " ")
		}
		if splitAt < 1 {
			splitAt = len(window)
		}

		chunks = append(chunks, strings.TrimSpace(content[:splitAt]))
		content = strings.TrimSpace(content[splitAt:])
	}
	if content != "" {
		chunks = append(chunks, content)
	}
	return chunks
}
