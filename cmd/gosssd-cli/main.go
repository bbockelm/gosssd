package main

import (
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/bbockelm/gosssd"
)

func main() {
	// Command line flags
	socketPath := flag.String("socket", gosssd.DefaultNSSSocketPath, "Path to SSSD NSS socket")
	timeout := flag.Duration("timeout", 5*time.Second, "Request timeout")
	username := flag.String("user", "", "Username to lookup")
	uid := flag.Uint("uid", 0, "UID to lookup")
	groupname := flag.String("group", "", "Group name to lookup")
	gid := flag.Uint("gid", 0, "GID to lookup")
	groups := flag.String("groups", "", "Get groups for username")
	flag.Parse()

	// Create client
	client := gosssd.NewClient(
		gosssd.WithSocketPath(*socketPath),
		gosssd.WithTimeout(*timeout),
	)

	// Connect
	if err := client.Connect(); err != nil {
		log.Fatalf("Failed to connect to SSSD: %v", err)
	}
	defer func() { _ = client.Close() }()

	// Execute requested operation
	switch {
	case *username != "":
		user, err := client.GetUserByName(*username)
		if err != nil {
			log.Fatalf("Failed to get user by name: %v", err)
		}
		printUser(user)
	case *uid != 0:
		user, err := client.GetUserByUID(uint32(*uid)) // #nosec G115 - uid flag is bounded by flag type
		if err != nil {
			log.Fatalf("Failed to get user by UID: %v", err)
		}
		printUser(user)
	case *groupname != "":
		group, err := client.GetGroupByName(*groupname)
		if err != nil {
			log.Fatalf("Failed to get group by name: %v", err)
		}
		printGroup(group)
	case *gid != 0:
		group, err := client.GetGroupByGID(uint32(*gid)) // #nosec G115 - gid flag is bounded by flag type
		if err != nil {
			log.Fatalf("Failed to get group by GID: %v", err)
		}
		printGroup(group)
	case *groups != "":
		gids, err := client.GetGroupsForUser(*groups)
		if err != nil {
			log.Fatalf("Failed to get groups for user: %v", err)
		}
		fmt.Printf("Groups for user '%s':\n", *groups)
		for _, gid := range gids {
			fmt.Printf("  GID: %d\n", gid)
		}
	default:
		flag.Usage()
		log.Fatal("Please specify an operation")
	}
}

func printUser(user *gosssd.User) {
	fmt.Printf("User Information:\n")
	fmt.Printf("  Username: %s\n", user.Name)
	fmt.Printf("  UID: %d\n", user.UID)
	fmt.Printf("  GID: %d\n", user.GID)
	fmt.Printf("  Home: %s\n", user.HomeDir)
	fmt.Printf("  Shell: %s\n", user.Shell)
	fmt.Printf("  GECOS: %s\n", user.Gecos)
}

func printGroup(group *gosssd.Group) {
	fmt.Printf("Group Information:\n")
	fmt.Printf("  Group Name: %s\n", group.Name)
	fmt.Printf("  GID: %d\n", group.GID)
	fmt.Printf("  Members: %v\n", group.Members)
}
