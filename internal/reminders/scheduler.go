package reminders

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/scheduler"
	"github.com/aws/aws-sdk-go-v2/service/scheduler/types"
)

const timezone = "Europe/Berlin"

type Payload struct {
	ChatID           int64   `json:"chat_id"`
	LetterID         string  `json:"letter_id"`
	Organization     string  `json:"organization"`
	DocType          string  `json:"doc_type"`
	Deadline         string  `json:"deadline"`
	ActionRequiredRU *string `json:"action_required_ru,omitempty"`
	Kind             string  `json:"kind"`
}

type SchedulerAPI interface {
	CreateSchedule(ctx context.Context, params *scheduler.CreateScheduleInput, optFns ...func(*scheduler.Options)) (*scheduler.CreateScheduleOutput, error)
	ListSchedules(ctx context.Context, params *scheduler.ListSchedulesInput, optFns ...func(*scheduler.Options)) (*scheduler.ListSchedulesOutput, error)
	DeleteSchedule(ctx context.Context, params *scheduler.DeleteScheduleInput, optFns ...func(*scheduler.Options)) (*scheduler.DeleteScheduleOutput, error)
}

type Scheduler struct {
	client          SchedulerAPI
	groupName       string
	targetLambdaArn string
	targetRoleArn   string
}

func NewScheduler(client SchedulerAPI, groupName, targetLambdaArn, targetRoleArn string) *Scheduler {
	return &Scheduler{
		client:          client,
		groupName:       groupName,
		targetLambdaArn: targetLambdaArn,
		targetRoleArn:   targetRoleArn,
	}
}

// ScheduleAll creates one EventBridge schedule per entry in list. Each
// schedule is named "<letterID>-<kind>" so it's traceable back to its source
// letter, and is configured with ActionAfterCompletion=DELETE so it cleans
// itself up after firing instead of accumulating in the schedule group
// forever (there is currently no code path that cancels a schedule if a
// letter is later deleted — see INTEGRATION_TESTS-style caveat in the PR).
//
// If creating one schedule fails partway through, ScheduleAll returns the
// error immediately without rolling back schedules already created — a
// partially-scheduled letter (e.g. only the 7-day-out reminder got created,
// not the 1-day-out one) is judged better than none, since it still reduces
// the chance of missing the deadline entirely.
func (s *Scheduler) ScheduleAll(ctx context.Context, letterID string, base Payload, list []Reminder) error {
	for _, r := range list {
		payload := base
		payload.Kind = string(r.Kind)

		body, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal reminder payload for %s/%s: %w", letterID, r.Kind, err)
		}

		name := fmt.Sprintf("%s-%s", letterID, r.Kind)
		_, err = s.client.CreateSchedule(ctx, &scheduler.CreateScheduleInput{
			Name:                       aws.String(name),
			GroupName:                  aws.String(s.groupName),
			ScheduleExpression:         aws.String(fmt.Sprintf("at(%s)", r.At.Format("2006-01-02T15:04:05"))),
			ScheduleExpressionTimezone: aws.String(timezone),
			ActionAfterCompletion:      types.ActionAfterCompletionDelete,
			FlexibleTimeWindow: &types.FlexibleTimeWindow{
				Mode: types.FlexibleTimeWindowModeOff,
			},
			Target: &types.Target{
				Arn:     aws.String(s.targetLambdaArn),
				RoleArn: aws.String(s.targetRoleArn),
				Input:   aws.String(string(body)),
			},
		})
		if err != nil {
			return fmt.Errorf("create schedule %s: %w", name, err)
		}
	}
	return nil
}

type ScheduledReminder struct {
	LetterID string
	Kind     string
	Name     string
}

func (s *Scheduler) ListForLetter(ctx context.Context, letterID string) ([]ScheduledReminder, error) {
	out, err := s.client.ListSchedules(ctx, &scheduler.ListSchedulesInput{
		GroupName:  aws.String(s.groupName),
		NamePrefix: aws.String(letterID + "-"),
	})
	if err != nil {
		return nil, fmt.Errorf("list schedules for letter %s: %w", letterID, err)
	}

	result := make([]ScheduledReminder, 0, len(out.Schedules))
	for _, item := range out.Schedules {
		name := aws.ToString(item.Name)
		result = append(result, ScheduledReminder{
			LetterID: letterID,
			Kind:     strings.TrimPrefix(name, letterID+"-"),
			Name:     name,
		})
	}
	return result, nil
}

func (s *Scheduler) Cancel(ctx context.Context, name string) error {
	_, err := s.client.DeleteSchedule(ctx, &scheduler.DeleteScheduleInput{
		Name:      aws.String(name),
		GroupName: aws.String(s.groupName),
	})
	if err != nil {
		var notFound *types.ResourceNotFoundException
		if errors.As(err, &notFound) {
			return nil
		}
		return fmt.Errorf("delete schedule %s: %w", name, err)
	}
	return nil
}
