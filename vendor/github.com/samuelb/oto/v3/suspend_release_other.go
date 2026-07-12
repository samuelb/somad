// Copyright 2021 The Oto Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build !darwin

package oto

// SuspendAndRelease suspends the entire audio play and, where supported,
// releases the underlying OS audio device. On platforms other than macOS it is
// identical to Suspend: either the backend already frees the device when
// suspended (ALSA parks its writer) or it exposes no separate release, so there
// is nothing extra to do. Resume reactivates playback after either call.
//
// This is a soma-fork addition; upstream oto has no equivalent. It is
// concurrent-safe.
func (c *Context) SuspendAndRelease() error {
	return c.Suspend()
}
