/*
Copyright 2014 Google Inc. All rights reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package factory

import (
	"net/http"
	"net/http/httptest"
	"path"
	"reflect"
	"testing"
	"time"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api/latest"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api/testapi"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client/cache"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/labels"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/runtime"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"
)

func TestCreate(t *testing.T) {
	handler := util.FakeHandler{
		StatusCode:   500,
		ResponseBody: "",
		T:            t,
	}
	server := httptest.NewServer(&handler)
	defer server.Close()
	client := client.NewOrDie(&client.Config{Host: server.URL, Version: testapi.Version()})
	factory := NewConfigFactory(client)
	factory.Create()
}

func TestCreateLists(t *testing.T) {
	factory := NewConfigFactory(nil)
	table := []struct {
		location string
		factory  func() *listWatch
	}{
		// Minion
		{
			location: "/api/" + testapi.Version() + "/minions",
			factory:  factory.createMinionLW,
		},
		// Assigned pod
		{
			location: "/api/" + testapi.Version() + "/pods?fields=DesiredState.Host!%3D",
			factory:  factory.createAssignedPodLW,
		},
		// Unassigned pod
		{
			location: "/api/" + testapi.Version() + "/pods?fields=DesiredState.Host%3D",
			factory:  factory.createUnassignedPodLW,
		},
	}

	for _, item := range table {
		handler := util.FakeHandler{
			StatusCode:   500,
			ResponseBody: "",
			T:            t,
		}
		server := httptest.NewServer(&handler)
		defer server.Close()
		factory.Client = client.NewOrDie(&client.Config{Host: server.URL, Version: testapi.Version()})
		// This test merely tests that the correct request is made.
		item.factory().List()
		handler.ValidateRequest(t, item.location, "GET", nil)
	}
}

func TestCreateWatches(t *testing.T) {
	factory := NewConfigFactory(nil)
	table := []struct {
		rv       string
		location string
		factory  func() *listWatch
	}{
		// Minion watch
		{
			rv:       "",
			location: "/api/" + testapi.Version() + "/watch/minions?resourceVersion=",
			factory:  factory.createMinionLW,
		}, {
			rv:       "0",
			location: "/api/" + testapi.Version() + "/watch/minions?resourceVersion=0",
			factory:  factory.createMinionLW,
		}, {
			rv:       "42",
			location: "/api/" + testapi.Version() + "/watch/minions?resourceVersion=42",
			factory:  factory.createMinionLW,
		},
		// Assigned pod watches
		{
			rv:       "",
			location: "/api/" + testapi.Version() + "/watch/pods?fields=DesiredState.Host!%3D&resourceVersion=",
			factory:  factory.createAssignedPodLW,
		}, {
			rv:       "42",
			location: "/api/" + testapi.Version() + "/watch/pods?fields=DesiredState.Host!%3D&resourceVersion=42",
			factory:  factory.createAssignedPodLW,
		},
		// Unassigned pod watches
		{
			rv:       "",
			location: "/api/" + testapi.Version() + "/watch/pods?fields=DesiredState.Host%3D&resourceVersion=",
			factory:  factory.createUnassignedPodLW,
		}, {
			rv:       "42",
			location: "/api/" + testapi.Version() + "/watch/pods?fields=DesiredState.Host%3D&resourceVersion=42",
			factory:  factory.createUnassignedPodLW,
		},
	}

	for _, item := range table {
		handler := util.FakeHandler{
			StatusCode:   500,
			ResponseBody: "",
			T:            t,
		}
		server := httptest.NewServer(&handler)
		defer server.Close()
		factory.Client = client.NewOrDie(&client.Config{Host: server.URL, Version: testapi.Version()})

		// This test merely tests that the correct request is made.
		item.factory().Watch(item.rv)
		handler.ValidateRequest(t, item.location, "GET", nil)
	}
}

func TestPollMinions(t *testing.T) {
	table := []struct {
		minions []api.Node
	}{
		{
			minions: []api.Node{
				{ObjectMeta: api.ObjectMeta{Name: "foo"}},
				{ObjectMeta: api.ObjectMeta{Name: "bar"}},
			},
		},
	}

	for _, item := range table {
		ml := &api.NodeList{Items: item.minions}
		handler := util.FakeHandler{
			StatusCode:   200,
			ResponseBody: runtime.EncodeOrDie(latest.Codec, ml),
			T:            t,
		}
		mux := http.NewServeMux()
		// FakeHandler musn't be sent requests other than the one you want to test.
		mux.Handle("/api/"+testapi.Version()+"/minions", &handler)
		server := httptest.NewServer(mux)
		defer server.Close()
		client := client.NewOrDie(&client.Config{Host: server.URL, Version: testapi.Version()})
		cf := NewConfigFactory(client)

		ce, err := cf.pollMinions()
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
			continue
		}
		handler.ValidateRequest(t, "/api/"+testapi.Version()+"/minions", "GET", nil)

		if e, a := len(item.minions), ce.Len(); e != a {
			t.Errorf("Expected %v, got %v", e, a)
		}
	}
}

func makeNamespaceURL(namespace, suffix string, isClient bool) string {
	if client.NamespaceInPathFor(testapi.Version()) {
		return makeURL("/ns/" + namespace + suffix)
	}
	// if this is a url the client should call, encode the url
	if isClient {
		return makeURL(suffix + "?namespace=" + namespace)
	}
	// its not a client url, so its what the server needs to listen on
	return makeURL(suffix)
}

func makeURL(suffix string) string {
	return path.Join("/api", testapi.Version(), suffix)
}

func TestDefaultErrorFunc(t *testing.T) {
	testPod := &api.Pod{ObjectMeta: api.ObjectMeta{Name: "foo", Namespace: "bar"}}
	handler := util.FakeHandler{
		StatusCode:   200,
		ResponseBody: runtime.EncodeOrDie(latest.Codec, testPod),
		T:            t,
	}
	mux := http.NewServeMux()

	// FakeHandler musn't be sent requests other than the one you want to test.
	mux.Handle(makeNamespaceURL("bar", "/pods/foo", false), &handler)
	server := httptest.NewServer(mux)
	defer server.Close()
	factory := NewConfigFactory(client.NewOrDie(&client.Config{Host: server.URL, Version: testapi.Version()}))
	queue := cache.NewFIFO()
	podBackoff := podBackoff{
		perPodBackoff: map[string]*backoffEntry{},
		clock:         &fakeClock{},
	}
	errFunc := factory.makeDefaultErrorFunc(&podBackoff, queue)

	errFunc(testPod, nil)
	for {
		// This is a terrible way to do this but I plan on replacing this
		// whole error handling system in the future. The test will time
		// out if something doesn't work.
		time.Sleep(10 * time.Millisecond)
		got, exists := queue.Get("foo")
		if !exists {
			continue
		}
		handler.ValidateRequest(t, makeNamespaceURL("bar", "/pods/foo", true), "GET", nil)
		if e, a := testPod, got; !reflect.DeepEqual(e, a) {
			t.Errorf("Expected %v, got %v", e, a)
		}
		break
	}
}

func TestStoreToMinionLister(t *testing.T) {
	store := cache.NewStore()
	ids := util.NewStringSet("foo", "bar", "baz")
	for id := range ids {
		store.Add(id, &api.Node{ObjectMeta: api.ObjectMeta{Name: id}})
	}
	sml := storeToNodeLister{store}

	gotNodes, err := sml.List()
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	got := make([]string, len(gotNodes.Items))
	for ix := range gotNodes.Items {
		got[ix] = gotNodes.Items[ix].Name
	}
	if !ids.HasAll(got...) || len(got) != len(ids) {
		t.Errorf("Expected %v, got %v", ids, got)
	}
}

func TestStoreToPodLister(t *testing.T) {
	store := cache.NewStore()
	ids := []string{"foo", "bar", "baz"}
	for _, id := range ids {
		store.Add(id, &api.Pod{
			ObjectMeta: api.ObjectMeta{
				Name:   id,
				Labels: map[string]string{"name": id},
			},
		})
	}
	spl := storeToPodLister{store}

	for _, id := range ids {
		got, err := spl.ListPods(labels.Set{"name": id}.AsSelector())
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
			continue
		}
		if e, a := 1, len(got); e != a {
			t.Errorf("Expected %v, got %v", e, a)
			continue
		}
		if e, a := id, got[0].Name; e != a {
			t.Errorf("Expected %v, got %v", e, a)
			continue
		}
	}
}

func TestMinionEnumerator(t *testing.T) {
	testList := &api.NodeList{
		Items: []api.Node{
			{ObjectMeta: api.ObjectMeta{Name: "foo"}},
			{ObjectMeta: api.ObjectMeta{Name: "bar"}},
			{ObjectMeta: api.ObjectMeta{Name: "baz"}},
		},
	}
	me := nodeEnumerator{testList}

	if e, a := 3, me.Len(); e != a {
		t.Fatalf("expected %v, got %v", e, a)
	}
	for i := range testList.Items {
		gotID, gotObj := me.Get(i)
		if e, a := testList.Items[i].Name, gotID; e != a {
			t.Errorf("Expected %v, got %v", e, a)
		}
		if e, a := &testList.Items[i], gotObj; !reflect.DeepEqual(e, a) {
			t.Errorf("Expected %#v, got %v#", e, a)
		}
	}
}

type fakeClock struct {
	t time.Time
}

func (f *fakeClock) Now() time.Time {
	return f.t
}

func TestBind(t *testing.T) {
	table := []struct {
		binding *api.Binding
	}{
		{binding: &api.Binding{PodID: "foo", Host: "foohost.kubernetes.mydomain.com"}},
	}

	for _, item := range table {
		handler := util.FakeHandler{
			StatusCode:   200,
			ResponseBody: "",
			T:            t,
		}
		server := httptest.NewServer(&handler)
		defer server.Close()
		client := client.NewOrDie(&client.Config{Host: server.URL, Version: testapi.Version()})
		b := binder{client}

		if err := b.Bind(item.binding); err != nil {
			t.Errorf("Unexpected error: %v", err)
			continue
		}
		expectedBody := runtime.EncodeOrDie(testapi.Codec(), item.binding)
		handler.ValidateRequest(t, "/api/"+testapi.Version()+"/bindings", "POST", &expectedBody)
	}
}

func TestBackoff(t *testing.T) {
	clock := fakeClock{}
	backoff := podBackoff{
		perPodBackoff: map[string]*backoffEntry{},
		clock:         &clock,
	}

	tests := []struct {
		podID            string
		expectedDuration time.Duration
		advanceClock     time.Duration
	}{
		{
			podID:            "foo",
			expectedDuration: 1 * time.Second,
		},
		{
			podID:            "foo",
			expectedDuration: 2 * time.Second,
		},
		{
			podID:            "foo",
			expectedDuration: 4 * time.Second,
		},
		{
			podID:            "bar",
			expectedDuration: 1 * time.Second,
			advanceClock:     120 * time.Second,
		},
		// 'foo' should have been gc'd here.
		{
			podID:            "foo",
			expectedDuration: 1 * time.Second,
		},
	}

	for _, test := range tests {
		duration := backoff.getBackoff(test.podID)
		if duration != test.expectedDuration {
			t.Errorf("expected: %s, got %s for %s", test.expectedDuration.String(), duration.String(), test.podID)
		}
		clock.t = clock.t.Add(test.advanceClock)
		backoff.gc()
	}

	backoff.perPodBackoff["foo"].backoff = 60 * time.Second
	duration := backoff.getBackoff("foo")
	if duration != 60*time.Second {
		t.Errorf("expected: 60, got %s", duration.String())
	}
}
