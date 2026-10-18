package tasks_test

import (
	"slices"
	"testing"

	"github.com/pmaxis/helpic/internal/tasks"
	"github.com/pmaxis/helpic/internal/user"
)

func TestNextStatuses(t *testing.T) {
	const freelancer, client = "f", "c"
	shared := func(status, createdBy string) tasks.Task {
		return tasks.Task{Status: status, CreatedBy: createdBy, Client: &tasks.Party{ID: client}}
	}

	cases := []struct {
		name   string
		task   tasks.Task
		role   string
		viewer string
		want   []string
	}{
		{"personal task goes anywhere", tasks.Task{Status: tasks.StatusInProgress, CreatedBy: freelancer},
			user.RoleFreelancer, freelancer, []string{tasks.StatusNew, tasks.StatusDone}},
		{"freelancer moves client work back to new", shared(tasks.StatusInProgress, client),
			user.RoleFreelancer, freelancer, []string{tasks.StatusNew, tasks.StatusDone}},
		{"freelancer moves their own shared task", shared(tasks.StatusNew, freelancer),
			user.RoleFreelancer, freelancer, []string{tasks.StatusInProgress, tasks.StatusDone}},
		{"client can't mark done", shared(tasks.StatusInProgress, client),
			user.RoleClient, client, nil},
	}
	for _, c := range cases {
		got := tasks.NextStatuses(c.task, c.role, c.viewer)
		slices.Sort(got)
		want := slices.Clone(c.want)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("%s: got %v, want %v", c.name, got, want)
		}
	}
}

func TestCanEdit(t *testing.T) {
	const freelancer, client = "f", "c"
	shared := func(status, createdBy string) tasks.Task {
		return tasks.Task{Status: status, CreatedBy: createdBy, Freelancer: tasks.Party{ID: freelancer}, Client: &tasks.Party{ID: client}}
	}
	cases := []struct {
		name   string
		task   tasks.Task
		viewer string
		want   bool
	}{
		{"creator edits an open shared task", shared(tasks.StatusInProgress, client), client, true},
		{"creator edits a done shared task", shared(tasks.StatusDone, client), client, true},
		{"freelancer edits what the client set", shared(tasks.StatusDone, client), freelancer, true},
		{"client can't edit what the freelancer set", shared(tasks.StatusNew, freelancer), client, false},
		{"a done personal task stays editable", tasks.Task{Status: tasks.StatusDone, CreatedBy: freelancer, Freelancer: tasks.Party{ID: freelancer}}, freelancer, true},
	}
	for _, c := range cases {
		if got := tasks.CanEdit(c.task, c.viewer); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestCanSeePrice(t *testing.T) {
	const freelancer, owner, member = "f", "o", "m"
	shared := tasks.Task{CreatedBy: member, Freelancer: tasks.Party{ID: freelancer}, Client: &tasks.Party{ID: owner}}
	personal := tasks.Task{CreatedBy: freelancer, Freelancer: tasks.Party{ID: freelancer}}
	cases := []struct {
		name   string
		task   tasks.Task
		viewer string
		want   bool
	}{
		{"the freelancer", shared, freelancer, true},
		{"the company's owner", shared, owner, true},
		{"a member, even the task's creator", shared, member, false},
		{"a personal task's freelancer", personal, freelancer, true},
	}
	for _, c := range cases {
		if got := tasks.CanSeePrice(c.task, c.viewer); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
