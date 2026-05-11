package modmail

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"example.com/ModMail/internal/config"
	"example.com/ModMail/internal/storage"
	"github.com/bwmarrin/discordgo"
)

const (
	closeCommandName  = "close"
	reopenCommandName = "reopen"
)

type Service struct {
	cfg   config.Config
	store *storage.Store
	botID string
}

func New(cfg config.Config, store *storage.Store) *Service {
	return &Service{cfg: cfg, store: store}
}

func (s *Service) RegisterHandlers(dg *discordgo.Session) {
	dg.AddHandler(s.onReady)
	dg.AddHandler(s.onMessageCreate)
	dg.AddHandler(s.onInteractionCreate)
}

func (s *Service) onReady(dg *discordgo.Session, r *discordgo.Ready) {
	s.botID = r.User.ID
	log.Printf("logged in as %s#%s", r.User.Username, r.User.Discriminator)
	if s.cfg.EnableSlash {
		s.registerCommands(dg)
	}
}

func (s *Service) registerCommands(dg *discordgo.Session) {
	commands := []*discordgo.ApplicationCommand{
		{Name: closeCommandName, Description: "Close the current modmail ticket"},
		{Name: reopenCommandName, Description: "Reopen the latest closed ticket in this channel"},
	}
	for _, cmd := range commands {
		if _, err := dg.ApplicationCommandCreate(dg.State.User.ID, s.cfg.GuildID, cmd); err != nil {
			log.Printf("register command %s: %v", cmd.Name, err)
		}
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

	switch i.ApplicationCommandData().Name {
	case closeCommandName:
		m := &discordgo.MessageCreate{Message: &discordgo.Message{ChannelID: i.ChannelID, Author: i.Member.User}}
		if err := s.handleClose(dg, m); err != nil {
			s.respondInteraction(dg, i, "Could not close this ticket.")
			return
		}
		s.respondInteraction(dg, i, "Ticket closed.")
	case reopenCommandName:
		m := &discordgo.MessageCreate{Message: &discordgo.Message{ChannelID: i.ChannelID, Author: i.Member.User}}
		if err := s.handleReopen(dg, m); err != nil {
			s.respondInteraction(dg, i, "Could not reopen this ticket.")
			return
		}
		s.respondInteraction(dg, i, "Ticket reopened.")
	}
}

func (s *Service) respondInteraction(dg *discordgo.Session, i *discordgo.InteractionCreate, msg string) {
	_ = dg.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: msg, Flags: discordgo.MessageFlagsEphemeral},
	})
}

func (s *Service) onMessageCreate(dg *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author == nil || m.Author.Bot {
		return
	}

	if m.GuildID == "" {
		if err := s.handleUserDM(dg, m); err != nil {
			log.Printf("handle user dm: %v", err)
			_ = s.sendMessageWithRetry(dg, m.ChannelID, "Sorry, something went wrong while opening your modmail ticket.")
		}
		return
	}

	if m.GuildID != s.cfg.GuildID || !s.isInStaffCategory(dg, m.ChannelID) {
		return
	}

	if strings.HasPrefix(m.Content, s.cfg.CommandPrefix+closeCommandName) {
		if err := s.handleClose(dg, m); err != nil {
			log.Printf("handle close: %v", err)
			_ = s.sendMessageWithRetry(dg, m.ChannelID, "Could not close this ticket.")
		}
		return
	}
	if strings.HasPrefix(m.Content, s.cfg.CommandPrefix+reopenCommandName) {
		if err := s.handleReopen(dg, m); err != nil {
			log.Printf("handle reopen: %v", err)
			_ = s.sendMessageWithRetry(dg, m.ChannelID, "Could not reopen this ticket.")
		}
		return
	}

	if err := s.handleStaffReply(dg, m); err != nil && !errors.Is(err, sql.ErrNoRows) {
		log.Printf("handle staff reply: %v", err)
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

func (s *Service) handleUserDM(dg *discordgo.Session, m *discordgo.MessageCreate) error {
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

		_ = s.sendMessageWithRetry(dg, m.ChannelID, "Your message has been sent to the staff team. Reply here to add more information.")
		_ = s.sendMessageWithRetry(dg, ch.ID, fmt.Sprintf("New modmail ticket from **%s** (`%s`). Use `%sclose` or `/close` to close.", userTag(m.Author), m.Author.ID, s.cfg.CommandPrefix))
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
	content := strings.TrimSpace(m.Content)
	if content == "" && len(m.Attachments) == 0 {
		return nil
	}

	builder := strings.Builder{}
	builder.WriteString(fmt.Sprintf("**%s:**", userTag(m.Author)))
	if content != "" {
		builder.WriteString("\n")
		builder.WriteString(content)
	}
	for _, a := range m.Attachments {
		builder.WriteString("\n")
		builder.WriteString(a.URL)
	}

	return s.sendMessageWithRetry(dg, channelID, builder.String())
}

func (s *Service) handleStaffReply(dg *discordgo.Session, m *discordgo.MessageCreate) error {
	ticket, err := s.store.GetOpenTicketByChannel(m.ChannelID)
	if err != nil {
		return err
	}

	content := strings.TrimSpace(m.Content)
	if content == "" && len(m.Attachments) == 0 {
		return nil
	}

	builder := strings.Builder{}
	builder.WriteString("**Staff:**")
	if content != "" {
		builder.WriteString("\n")
		builder.WriteString(content)
	}
	for _, a := range m.Attachments {
		builder.WriteString("\n")
		builder.WriteString(a.URL)
	}

	userCh, err := dg.UserChannelCreate(ticket.UserID)
	if err != nil {
		return err
	}
	return s.sendMessageWithRetry(dg, userCh.ID, builder.String())
}

func (s *Service) handleClose(dg *discordgo.Session, m *discordgo.MessageCreate) error {
	ticket, err := s.store.GetOpenTicketByChannel(m.ChannelID)
	if err != nil {
		return err
	}

	transcript, transcriptErr := s.buildTranscript(dg, m.ChannelID)
	if transcriptErr != nil {
		log.Printf("build transcript: %v", transcriptErr)
	}

	if err := s.store.CloseTicket(m.ChannelID); err != nil {
		return err
	}

	userCh, err := dg.UserChannelCreate(ticket.UserID)
	if err == nil {
		_ = s.sendMessageWithRetry(dg, userCh.ID, "Your modmail ticket has been closed. Send another message here to open a new ticket.")
	}

	_ = s.sendMessageWithRetry(dg, m.ChannelID, "Ticket closed.")
	if s.cfg.LogChannelID != "" {
		logMsg := fmt.Sprintf("Ticket `%d` for user `%s` was closed by %s.", ticket.ID, ticket.UserID, userTag(m.Author))
		_ = s.sendMessageWithRetry(dg, s.cfg.LogChannelID, logMsg)
		if len(transcript) > 0 {
			_, _ = dg.ChannelFileSend(s.cfg.LogChannelID, fmt.Sprintf("ticket-%d-transcript.txt", ticket.ID), bytes.NewReader(transcript))
		}
	}
	if s.cfg.AutoDeleteAfter > 0 {
		_ = s.sendMessageWithRetry(dg, m.ChannelID, fmt.Sprintf("This channel will be deleted in %s.", s.cfg.AutoDeleteAfter))
		go func(chID string, wait time.Duration) {
			time.Sleep(wait)
			_, _ = dg.ChannelDelete(chID)
		}(m.ChannelID, s.cfg.AutoDeleteAfter)
	}

	return nil
}

func (s *Service) handleReopen(dg *discordgo.Session, m *discordgo.MessageCreate) error {
	ticket, err := s.store.GetLatestTicketByChannel(m.ChannelID)
	if err != nil {
		return err
	}
	if ticket.Status == "open" {
		return s.sendMessageWithRetry(dg, m.ChannelID, "Ticket is already open.")
	}
	if err := s.store.ReopenTicket(m.ChannelID); err != nil {
		if errors.Is(err, storage.ErrOpenTicketExists) {
			return s.sendMessageWithRetry(dg, m.ChannelID, "Cannot reopen: this user already has another open ticket.")
		}
		return err
	}

	userCh, err := dg.UserChannelCreate(ticket.UserID)
	if err == nil {
		_ = s.sendMessageWithRetry(dg, userCh.ID, "Your modmail ticket has been reopened by staff.")
	}
	return s.sendMessageWithRetry(dg, m.ChannelID, "Ticket reopened.")
}

func (s *Service) buildTranscript(dg *discordgo.Session, channelID string) ([]byte, error) {
	var all []*discordgo.Message
	before := ""
	for {
		batch, err := dg.ChannelMessages(channelID, 100, before, "", "")
		if err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			break
		}
		all = append(all, batch...)
		before = batch[len(batch)-1].ID
		if len(batch) < 100 || len(all) >= 500 {
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
	for _, msg := range all {
		author := "unknown"
		if msg.Author != nil {
			author = userTag(msg.Author)
		}
		b.WriteString(fmt.Sprintf("[%s] %s\n", msg.Timestamp.Format(time.RFC3339), author))
		if strings.TrimSpace(msg.Content) != "" {
			b.WriteString(msg.Content)
			b.WriteString("\n")
		}
		for _, a := range msg.Attachments {
			b.WriteString(a.URL)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	return []byte(b.String()), nil
}

func (s *Service) sendMessageWithRetry(dg *discordgo.Session, channelID, content string) error {
	var lastErr error
	for i := 0; i < 3; i++ {
		_, err := dg.ChannelMessageSend(channelID, content)
		if err == nil {
			return nil
		}
		lastErr = err
		time.Sleep(time.Duration(i+1) * 250 * time.Millisecond)
	}
	return lastErr
}

func userTag(u *discordgo.User) string {
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
	if len(out) > 90 {
		out = out[:90]
	}
	return out
}
