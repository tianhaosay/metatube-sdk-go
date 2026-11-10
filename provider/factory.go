package provider

import (
	"fmt"
	"strings"
	"sync"
)

type (
	MovieFactory = func() MovieProvider
	ActorFactory = func() ActorProvider
)

var (
	// Factory RW Mutex
	factoryMu sync.RWMutex
	// Actor/Movie Factories
	movieFactories = make(map[string]MovieFactory)
	actorFactories = make(map[string]ActorFactory)
)

func Register[T Provider](name string, factory func() T) {
	factoryMu.Lock()
	defer factoryMu.Unlock()

	provider := *new(T)
	registered := false

	if _, ok := any(provider).(ActorProvider); ok {
		actorFactories[name] = func() ActorProvider { return any(factory()).(ActorProvider) }
		registered = true
	}

	if _, ok := any(provider).(MovieProvider); ok {
		movieFactories[name] = func() MovieProvider { return any(factory()).(MovieProvider) }
		registered = true
	}

	if !registered {
		panic(fmt.Sprintf("invalid provider factory: func() %T", provider))
	}
}

func RangeMovieFactory(f func(string, MovieFactory) bool) {
	factoryMu.RLock()
	for name, factory := range movieFactories {
		if !f(name, factory) {
			return
		}
	}
	factoryMu.RUnlock()
}

func RangeActorFactory(f func(string, ActorFactory) bool) {
	factoryMu.RLock()
	for name, factory := range actorFactories {
		if !f(name, factory) {
			return
		}
	}
	factoryMu.RUnlock()
}

// GetMovieProviderByName returns a new MovieProvider by case-insensitive name.
func GetMovieProviderByName(name string) (MovieProvider, error) {
	factoryMu.RLock()
	defer factoryMu.RUnlock()
	for n, factory := range movieFactories {
		if strings.EqualFold(n, name) {
			return factory(), nil
		}
	}
	return nil, ErrProviderNotFound
}

// GetActorProviderByName returns a new ActorProvider by case-insensitive name.
func GetActorProviderByName(name string) (ActorProvider, error) {
	factoryMu.RLock()
	defer factoryMu.RUnlock()
	for n, factory := range actorFactories {
		if strings.EqualFold(n, name) {
			return factory(), nil
		}
	}
	return nil, ErrProviderNotFound
}
