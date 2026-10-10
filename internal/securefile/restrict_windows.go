//go:build windows

package securefile

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// restrictToOwner replaces the file's inherited access-control list with a
// protected one granting only the current user, SYSTEM, and Administrators:
// the same principals a user profile folder admits. Protection stops the
// parent folder's entries, such as Users or Authenticated Users on a folder
// under the drive root, from flowing back in.
func restrictToOwner(path string) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("read the current user: %w", err)
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return fmt.Errorf("resolve SYSTEM: %w", err)
	}
	administrators, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return fmt.Errorf("resolve Administrators: %w", err)
	}

	entries := []windows.EXPLICIT_ACCESS{
		fullControl(user.User.Sid, windows.TRUSTEE_IS_USER),
		fullControl(system, windows.TRUSTEE_IS_WELL_KNOWN_GROUP),
		fullControl(administrators, windows.TRUSTEE_IS_WELL_KNOWN_GROUP),
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		for i := range entries {
			entries[i].Inheritance = windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT
		}
	}
	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		return fmt.Errorf("build the access list: %w", err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, acl, nil); err != nil {
		return fmt.Errorf("apply the access list: %w", err)
	}
	return nil
}

func fullControl(sid *windows.SID, trusteeType windows.TRUSTEE_TYPE) windows.EXPLICIT_ACCESS {
	return windows.EXPLICIT_ACCESS{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  trusteeType,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}
}
