package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

type counterSorter struct {
	resources []*Resource
}

func (cs counterSorter) Len() int {
	return len(cs.resources)
}

func (cs counterSorter) Swap(i, j int) {
	cs.resources[i], cs.resources[j] = cs.resources[j], cs.resources[i]
}

func (cs counterSorter) Less(i, j int) bool {
	return cs.resources[i].counter < cs.resources[j].counter
}

type allGroup struct {
	Hosts []string               `json:"hosts"`
	Vars  map[string]interface{} `json:"vars"`
}

type AnsibleInv struct {
	Children []string                  `json:"children"`
}

type AnsibleMeta struct {
	HostVars map[string]interface{}    `json:"hostvars"`
}

type AnsibleGroup struct {
	Group    map[string][]string
}

func appendUniq(strs []string, item string) []string {
	if len(strs) == 0 {
		strs = append(strs, item)
		return strs
	}
	sort.Strings(strs)
	i := sort.SearchStrings(strs, item)
	if i == len(strs) || (i < len(strs) && strs[i] != item) {
		strs = append(strs, item)
	}
	return strs
}

func gatherAnsibleResources(s *state) map[string]interface{} {
    outputGroups := make(map[string]interface{})

    all := &AnsibleInv{Children: make([]string, 0)}
    meta := &AnsibleMeta{HostVars: make(map[string]interface{})}
    ansible_group := &AnsibleGroup{Group: make(map[string][]string)}

    for _, res := range s.resources() {
        if res.resourceType == "aws_instance" {
            // place in list of all resources
            // all.Hosts = appendUniq(all.Hosts, res.Address())
            group_name := res.Attributes()["tags.ansible_group"]
			host_name := fmt.Sprintf("%s.%s.aws", res.baseName, res.Attributes()["availability_zone"])
            all.Children = appendUniq(all.Children, group_name)
            ansible_group.Group[group_name] = append(ansible_group.Group[group_name], host_name)
            // inventorize outputs as variables
            if len(s.outputs()) > 0 {
                hostvars := make(map[string]interface{})
                for _, out := range s.outputs() {
                    if strings.Contains(out.keyName, fmt.Sprintf("%s_%s", res.resourceType, res.baseName)) {
					    var n string
                        keyname := strings.Replace(out.keyName, fmt.Sprintf("%s_%s_", res.resourceType, res.baseName), n, 1)
                        hostvars[keyname]=out.value
                    }
                }
                meta.HostVars[host_name] = hostvars
            }
        }
    }

    outputGroups["all"] = all
    outputGroups["_meta"] = meta

    for k, v := range ansible_group.Group {
	    group_host := make(map[string][]string)
		group_host["hosts"] = v
        outputGroups[k] = group_host
    }

	return outputGroups
}


func gatherResources(s *state) map[string]interface{} {
	outputGroups := make(map[string]interface{})

	all := &allGroup{Hosts: make([]string, 0), Vars: make(map[string]interface{})}
	types := make(map[string][]string)
	individual := make(map[string][]string)
	ordered := make(map[string][]string)
	tags := make(map[string][]string)

	unsortedOrdered := make(map[string][]*Resource)

	for _, res := range s.resources() {
		// place in list of all resources
		all.Hosts = appendUniq(all.Hosts, res.Address())

		// place in list of resource types
		tp := fmt.Sprintf("type_%s", res.resourceType)
		types[tp] = appendUniq(types[tp], res.Address())

		unsortedOrdered[res.baseName] = append(unsortedOrdered[res.baseName], res)

		// store as invdividual host (eg. <name>.<count>)
		invdName := fmt.Sprintf("%s.%d", res.baseName, res.counter)
		if old, exists := individual[invdName]; exists {
			fmt.Fprintf(os.Stderr, "overwriting already existing individual key %s, old: %v, new: %v", invdName, old, res.Address())
		}
		individual[invdName] = []string{res.Address()}

		// inventorize tags
		for k, v := range res.Tags() {
			// Valueless
			tag := k
			if v != "" {
				tag = fmt.Sprintf("%s_%s", k, v)
			}
			tags[tag] = appendUniq(tags[tag], res.Address())
		}
	}

	// inventorize outputs as variables
	if len(s.outputs()) > 0 {
		for _, out := range s.outputs() {
			all.Vars[out.keyName] = out.value
		}
	}

	// sort the ordered groups
	for basename, resources := range unsortedOrdered {
		cs := counterSorter{resources}
		sort.Sort(cs)

		for i := range resources {
			ordered[basename] = append(ordered[basename], resources[i].Address())
		}
	}

	outputGroups["all"] = all
	for k, v := range individual {
		if old, exists := outputGroups[k]; exists {
			fmt.Fprintf(os.Stderr, "individual overwriting already existing output with key %s, old: %v, new: %v", k, old, v)
		}
		outputGroups[k] = v
	}
	for k, v := range ordered {
		if old, exists := outputGroups[k]; exists {
			fmt.Fprintf(os.Stderr, "ordered overwriting already existing output with key %s, old: %v, new: %v", k, old, v)
		}
		outputGroups[k] = v
	}
	for k, v := range types {
		if old, exists := outputGroups[k]; exists {
			fmt.Fprintf(os.Stderr, "types overwriting already existing output key %s, old: %v, new: %v", k, old, v)
		}
		outputGroups[k] = v
	}
	for k, v := range tags {
		if old, exists := outputGroups[k]; exists {
			fmt.Fprintf(os.Stderr, "tags overwriting already existing output key %s, old: %v, new: %v", k, old, v)
		}
		outputGroups[k] = v
	}

	return outputGroups
}

func cmdList(stdout io.Writer, stderr io.Writer, s *state) int {
	return output(stdout, stderr, gatherAnsibleResources(s))
}

func cmdInventory(stdout io.Writer, stderr io.Writer, s *state) int {
	groups := gatherResources(s)
	group_names := []string{}
	for group, _ := range groups {
		group_names = append(group_names, group)
	}
	sort.Strings(group_names)
	for _, group := range group_names {

		switch grp := groups[group].(type) {
		case []string:
			writeLn("["+group+"]", stdout, stderr)
			for _, item := range grp {
				writeLn(item, stdout, stderr)
			}

		case *allGroup:
			writeLn("["+group+"]", stdout, stderr)
			for _, item := range grp.Hosts {
				writeLn(item, stdout, stderr)
			}
			writeLn("", stdout, stderr)
			writeLn("["+group+":vars]", stdout, stderr)
			vars := []string{}
			for key, _ := range grp.Vars {
				vars = append(vars, key)
			}
			sort.Strings(vars)
			for _, key := range vars {
				jsonItem, _ := json.Marshal(grp.Vars[key])
				itemLn := fmt.Sprintf("%s", string(jsonItem))
				writeLn(key+"="+itemLn, stdout, stderr)
			}
		}

		writeLn("", stdout, stderr)
	}

	return 0
}

func writeLn(str string, stdout io.Writer, stderr io.Writer) {
	_, err := io.WriteString(stdout, str+"\n")
	checkErr(err, stderr)
}

func checkErr(err error, stderr io.Writer) int {
	if err != nil {
		fmt.Fprintf(stderr, "Error writing inventory: %s\n", err)
		return 1
	}
	return 0
}

func cmdHost(stdout io.Writer, stderr io.Writer, s *state, hostname string) int {
	for _, res := range s.resources() {
		if hostname == res.Address() {
			return output(stdout, stderr, res.Attributes())
		}
	}

	fmt.Fprintf(stdout, "{}")
	return 1
}

// output marshals an arbitrary JSON object and writes it to stdout, or writes
// an error to stderr, then returns the appropriate exit code.
func output(stdout io.Writer, stderr io.Writer, whatever interface{}) int {
	b, err := json.Marshal(whatever)
	if err != nil {
		fmt.Fprintf(stderr, "Error encoding JSON: %s\n", err)
		return 1
	}

	_, err = stdout.Write(b)
	if err != nil {
		fmt.Fprintf(stderr, "Error writing JSON: %s\n", err)
		return 1
	}

	return 0
}
