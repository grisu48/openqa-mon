/* openQA related methods and functions for revtui */
package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/os-autoinst/gopenqa"
)

func isJobTooOld(job gopenqa.Job, maxlifetime int64) bool {
	if maxlifetime <= 0 {
		return false
	}
	if job.Tfinished == "" {
		return false
	}
	tfinished, err := time.Parse("2006-01-02T15:04:05", job.Tfinished)
	if err != nil {
		return false
	}
	deltaT := time.Now().Unix() - tfinished.Unix()
	return deltaT > maxlifetime
}

func isReviewed(job gopenqa.Job, model *TUIModel, checkParallel bool) (bool, error) {
	reviewed, err := checkReviewed(job.ID, model.Instance)
	if err != nil || reviewed {
		return reviewed, err
	}

	// If not reviewed but "parallel_failed", check parallel jobs if they are reviewed
	if checkParallel {
		for _, childID := range job.Children.Parallel {
			reviewed, err := checkReviewed(childID, model.Instance)
			if err != nil {
				return reviewed, err
			}
			if reviewed {
				return true, nil
			}
		}
	}
	return false, nil
}

func checkReviewed(job int64, instance *gopenqa.Instance) (bool, error) {
	comments, err := instance.GetComments(job)
	if err != nil {
		return false, err
	}
	for _, c := range comments {
		if len(c.BugRefs) > 0 {
			return true, nil
		}
		// Manually check for poo or bsc reference
		if strings.Contains(c.Text, "poo#") || strings.Contains(c.Text, "bsc#") || strings.Contains(c.Text, "boo#") {
			return true, nil
		}
		// Or for link to progress/bugzilla ticket
		if strings.Contains(c.Text, "://progress.opensuse.org/issues/") || strings.Contains(c.Text, "://bugzilla.suse.com/show_bug.cgi?id=") || strings.Contains(c.Text, "://bugzilla.opensuse.org/show_bug.cgi?id=") {
			return true, nil
		}
	}
	return false, nil
}

func FetchJobGroups(instance *gopenqa.Instance) (map[int]gopenqa.JobGroup, error) {
	jobGroups := make(map[int]gopenqa.JobGroup)
	groups, err := instance.GetJobGroups()
	if err != nil {
		return jobGroups, err
	}
	for _, jg := range groups {
		jobGroups[jg.ID] = jg
	}
	return jobGroups, nil
}

/* Get job or clone current job of the given job ID */
func FetchJob(id int64, instance *gopenqa.Instance) (gopenqa.Job, error) {
	var job gopenqa.Job
	for i := 0; i < 25; i++ { // Max recursion depth is 25
		var err error
		job, err = instance.GetJob(id)
		if err != nil {
			return job, err
		}
		if job.IsCloned() {
			id = job.CloneID
			time.Sleep(100 * time.Millisecond) // Don't spam the instance
			continue
		}
		return job, nil
	}
	return job, fmt.Errorf("max recursion depth reached")
}

/* Fetch the given jobs and follow their clones */
func fetchJobsFollow(ids []int64, model *TUIModel, progress func(i, n int)) ([]gopenqa.Job, error) {
	// Obey the maximum number of job per requests.
	// We split the job ids into multiple requests if necessary
	jobs := make([]gopenqa.Job, 0)
	// Progress variables
	limit := model.Config.RequestJobLimit
	if limit <= 0 {
		limit = len(ids)
	}
	chunks := len(ids) / limit
	for i := 0; len(ids) > 0; i++ { // Repeat until no more ids are available.
		n := min(limit, len(ids))
		chunk, err := model.Instance.GetJobsFollow(ids[:n])
		if progress != nil {
			progress(i, chunks)
		}
		ids = ids[n:]
		if err != nil {
			return jobs, err
		}
		jobs = append(jobs, chunk...)
	}

	return jobs, nil
}

/* Fetch the given jobs from the instance at once */
func fetchJobs(ids []int64, model *TUIModel) ([]gopenqa.Job, error) {
	// Obey the maximum number of job per requests.
	// We split the job ids into multiple requests if necessary
	jobs := make([]gopenqa.Job, 0)
	for len(ids) > 0 {
		n := len(ids)
		if model.Config.RequestJobLimit > 0 {
			n = min(model.Config.RequestJobLimit, len(ids))
		}
		n = max(1, n)
		chunk, err := model.Instance.GetJobs(ids[:n])
		ids = ids[n:]
		if err != nil {
			return jobs, err
		}
		jobs = append(jobs, chunk...)
	}

	// Get cloned jobs, if present
	for i, job := range jobs {
		if job.IsCloned() {
			if job, err := FetchJob(job.ID, model.Instance); err != nil {
				return jobs, err
			} else {
				jobs[i] = job
			}
		}
	}
	return jobs, nil
}

type FetchJobsCallback func(int, int, int, int)

func FetchJobs(model *TUIModel, callback FetchJobsCallback) ([]gopenqa.Job, error) {
	ret := make([]gopenqa.Job, 0)
	for i, group := range model.Config.Groups {
		params := group.Params
		jobs, err := model.Instance.GetOverview("", params)
		if err != nil {
			return ret, err
		}

		// Limit jobs to at most MaxJobs
		if len(jobs) > model.Config.MaxJobs {
			jobs = jobs[:model.Config.MaxJobs]
		}

		// Get detailed job instances. Fetch them at once
		ids := make([]int64, 0)
		for _, job := range jobs {
			ids = append(ids, job.ID)
		}
		if callback != nil {
			// Add one to the counter to indicate the progress to humans (0/16 looks weird)
			callback(i+1, len(model.Config.Groups), 0, len(jobs))
		}
		jobs, err = fetchJobs(ids, model)
		if err != nil {
			return jobs, err
		}
		for _, job := range jobs {
			// Filter too old jobs and jobs with group=0
			if job.GroupID == 0 || !isJobTooOld(job, group.MaxLifetime) {
				ret = append(ret, job)
			}
		}
	}
	return ret, nil
}
