// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package app

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/shared/mlog"
	"github.com/mattermost/mattermost/server/public/shared/request"
)

// createDefaultChannelMemberships adds users to channels based on their group memberships and how those groups are
// configured to sync with channels for group members on or after the given timestamp. If a channelID is given
// only that channel's members are created. If channelID is nil all channel memberships are created.
// If includeRemovedMembers is true, then channel members who left or were removed from the channel will
// be re-added; otherwise, they will not be re-added.
func (a *App) createDefaultChannelMemberships(c request.CTX, params model.CreateDefaultMembershipParams) error {
	channelMembers, appErr := a.ChannelMembersToAdd(params.Since, params.ScopedChannelID, params.ReAddRemovedMembers)
	if appErr != nil {
		return appErr
	}

	for _, userChannel := range channelMembers {
		if params.ScopedUserID != nil && *params.ScopedUserID != userChannel.UserID {
			continue
		}
		channel, err := a.GetChannel(c, userChannel.ChannelID)
		if err != nil {
			return err
		}

		tmem, err := a.GetTeamMember(c, channel.TeamId, userChannel.UserID)
		if err != nil && err.Id != "app.team.get_member.missing.app_error" {
			return err
		}

		// First add user to team
		if tmem == nil {
			_, err = a.AddTeamMember(c, channel.TeamId, userChannel.UserID)
			if err != nil {
				if err.Id == "api.team.join_user_to_team.allowed_domains.app_error" {
					c.Logger().Info("User not added to channel - the domain associated with the user is not in the list of allowed team domains",
						mlog.String("user_id", userChannel.UserID),
						mlog.String("channel_id", userChannel.ChannelID),
						mlog.String("team_id", channel.TeamId),
					)
					continue
				}
				return err
			}
			c.Logger().Info("added teammember",
				mlog.String("user_id", userChannel.UserID),
				mlog.String("team_id", channel.TeamId),
			)
		}

		_, err = a.AddChannelMember(c, userChannel.UserID, channel, ChannelMemberOpts{
			SkipTeamMemberIntegrityCheck: true,
		})
		if err != nil {
			if err.Id == "api.channel.add_user.to.channel.failed.deleted.app_error" {
				c.Logger().Info("Not adding user to channel because they have already left the team",
					mlog.String("user_id", userChannel.UserID),
					mlog.String("channel_id", userChannel.ChannelID),
				)
			} else {
				return err
			}
		}

		c.Logger().Info("added channelmember",
			mlog.String("user_id", userChannel.UserID),
			mlog.String("channel_id", userChannel.ChannelID),
		)
	}

	return nil
}

// createDefaultTeamMemberships adds users to teams based on their group memberships and how those groups are
// configured to sync with teams for group members on or after the given timestamp. If a teamID is given
// only that team's members are created. If teamID is nil all team memberships are created.
// If includeRemovedMembers is true, then team members who left or were removed from the team will
// be re-added; otherwise, they will not be re-added.
func (a *App) createDefaultTeamMemberships(c request.CTX, params model.CreateDefaultMembershipParams) error {
	teamMembers, appErr := a.TeamMembersToAdd(params.Since, params.ScopedTeamID, params.ReAddRemovedMembers)
	if appErr != nil {
		return appErr
	}

	for _, userTeam := range teamMembers {
		if params.ScopedUserID != nil && *params.ScopedUserID != userTeam.UserID {
			continue
		}
		_, err := a.AddTeamMember(c, userTeam.TeamID, userTeam.UserID)
		if err != nil {
			if err.Id == "api.team.join_user_to_team.allowed_domains.app_error" {
				c.Logger().Info("User not added to team - the domain associated with the user is not in the list of allowed team domains",
					mlog.String("user_id", userTeam.UserID),
					mlog.String("team_id", userTeam.TeamID),
				)
				continue
			}
			return err
		}

		c.Logger().Info("added teammember",
			mlog.String("user_id", userTeam.UserID),
			mlog.String("team_id", userTeam.TeamID),
		)
	}

	return nil
}

// CreateDefaultMemberships adds users to teams and channels based on their group memberships and how those groups
// are configured to sync with teams and channels for group members on or after the given timestamp.
// If includeRemovedMembers is true, then members who left or were removed from a team/channel will
// be re-added; otherwise, they will not be re-added.
func (a *App) CreateDefaultMemberships(c *request.Context, params model.CreateDefaultMembershipParams) error {
	err := a.createDefaultTeamMemberships(c, params)
	if err != nil {
		return err
	}

	err = a.createDefaultChannelMemberships(c, params)
	if err != nil {
		return err
	}

	return nil
}

// DeleteGroupConstrainedMemberships deletes team and channel memberships of users who aren't members of the allowed
// groups of all group-constrained teams and channels.
func (a *App) DeleteGroupConstrainedMemberships(c *request.Context) error {
	err := a.deleteGroupConstrainedChannelMemberships(c, nil)
	if err != nil {
		return err
	}

	err = a.deleteGroupConstrainedTeamMemberships(c, nil)
	if err != nil {
		return err
	}

	return nil
}

// deleteGroupConstrainedTeamMemberships deletes team memberships of users who aren't members of the allowed
// groups of the given group-constrained team. If a teamID is given then the procedure is scoped to the given team,
// if teamID is nil then the procedure affects all teams.
func (a *App) deleteGroupConstrainedTeamMemberships(c request.CTX, teamID *string) error {
	teamMembers, appErr := a.TeamMembersToRemove(teamID)
	if appErr != nil {
		return appErr
	}

	for _, userTeam := range teamMembers {
		err := a.RemoveUserFromTeam(c, userTeam.TeamId, userTeam.UserId, "")
		if err != nil {
			return err
		}

		c.Logger().Info("removed teammember",
			mlog.String("user_id", userTeam.UserId),
			mlog.String("team_id", userTeam.TeamId),
		)
	}

	return nil
}

// deleteGroupConstrainedChannelMemberships deletes channel memberships of users who aren't members of the allowed
// groups of the given group-constrained channel. If a channelID is given then the procedure is scoped to the given team,
// if channelID is nil then the procedure affects all teams.
func (a *App) deleteGroupConstrainedChannelMemberships(c request.CTX, channelID *string) error {
	channelMembers, appErr := a.ChannelMembersToRemove(channelID)
	if appErr != nil {
		return appErr
	}

	for _, userChannel := range channelMembers {
		channel, err := a.GetChannel(c, userChannel.ChannelId)
		if err != nil {
			return err
		}

		err = a.RemoveUserFromChannel(c, userChannel.UserId, "", channel)
		if err != nil {
			return err
		}

		a.Log().Info("removed channelmember",
			mlog.String("user_id", userChannel.UserId),
			mlog.String("channel_id", channel.Id),
		)
	}

	return nil
}

// SyncSyncableRoles updates the SchemeAdmin field value of the given syncable's members based on the configuration of
// the member's group memberships and the configuration of those groups to the syncable. This method should only
// be invoked on group-synced (aka group-constrained) syncables.
func (a *App) SyncSyncableRoles(syncableID string, syncableType model.GroupSyncableType) *model.AppError {
	permittedAdmins, err := a.Srv().Store().Group().PermittedSyncableAdmins(syncableID, syncableType)
	if err != nil {
		return model.NewAppError("SyncSyncableRoles", "app.select_error", nil, "", http.StatusInternalServerError).Wrap(err)
	}

	a.Log().Info(
		fmt.Sprintf("Permitted admins for %s", syncableType),
		mlog.String(strings.ToLower(fmt.Sprintf("%s_id", syncableType)), syncableID),
		mlog.Any("permitted_admins", permittedAdmins),
	)

	switch syncableType {
	case model.GroupSyncableTypeTeam:
		nErr := a.Srv().Store().Team().UpdateMembersRole(syncableID, permittedAdmins)
		if nErr != nil {
			return model.NewAppError("App.SyncSyncableRoles", "app.update_error", nil, "", http.StatusInternalServerError).Wrap(nErr)
		}
		return nil
	case model.GroupSyncableTypeChannel:
		nErr := a.Srv().Store().Channel().UpdateMembersRole(syncableID, permittedAdmins)
		if nErr != nil {
			return model.NewAppError("App.SyncSyncableRoles", "app.update_error", nil, "", http.StatusInternalServerError).Wrap(nErr)
		}
		return nil
	default:
		return model.NewAppError("App.SyncSyncableRoles", "groups.unsupported_syncable_type", map[string]any{"Value": syncableType}, "", http.StatusInternalServerError)
	}
}

// SyncRolesAndMembership updates the SchemeAdmin status and membership of all of the members of the given
// syncable.
func (a *App) SyncRolesAndMembership(c request.CTX, syncableID string, syncableType model.GroupSyncableType, includeRemovedMembers bool) {
	a.SyncSyncableRoles(syncableID, syncableType)

	lastJob, _ := a.Srv().Store().Job().GetNewestJobByStatusAndType(model.JobStatusSuccess, model.JobTypeLdapSync)
	var since int64
	if lastJob != nil {
		since = lastJob.StartAt
	}

	params := model.CreateDefaultMembershipParams{Since: since, ReAddRemovedMembers: includeRemovedMembers}

	switch syncableType {
	case model.GroupSyncableTypeTeam:
		params.ScopedTeamID = &syncableID
		a.createDefaultTeamMemberships(c, params)
		a.deleteGroupConstrainedTeamMemberships(c, &syncableID)
		if err := a.ClearTeamMembersCache(syncableID); err != nil {
			c.Logger().Warn("Error clearing team members cache", mlog.Err(err))
		}
	case model.GroupSyncableTypeChannel:
		params.ScopedChannelID = &syncableID
		a.createDefaultChannelMemberships(c, params)
		a.deleteGroupConstrainedChannelMemberships(c, &syncableID)
		if err := a.ClearChannelMembersCache(c, syncableID); err != nil {
			c.Logger().Warn("Error clearing channel members cache", mlog.Err(err))
		}
	}
}
