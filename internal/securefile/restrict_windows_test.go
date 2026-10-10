//go:build windows

package securefile

import (
	"path/filepath"
	"slices"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// On Windows the private files must not inherit the folder's permissions:
// under a folder such as C:\Brainslug those include every local user.
func TestPrivateFilesAdmitOnlyOwnerSystemAndAdministrators(t *testing.T) {
	directory := t.TempDir()
	written := filepath.Join(directory, "brainslug.provider-auth.json")
	if err := Write(written, []byte("{}")); err != nil {
		t.Fatal(err)
	}
	created := filepath.Join(directory, "brainslug.admin-auth.json")
	if err := CreateExclusive(created, []byte("{}")); err != nil {
		t.Fatal(err)
	}

	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		t.Fatal(err)
	}
	administrators, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		t.Fatal(err)
	}
	allowed := []*windows.SID{user.User.Sid, system, administrators}

	for _, path := range []string{written, created} {
		t.Run(filepath.Base(path), func(t *testing.T) {
			descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
			if err != nil {
				t.Fatal(err)
			}
			control, _, err := descriptor.Control()
			if err != nil {
				t.Fatal(err)
			}
			if control&windows.SE_DACL_PROTECTED == 0 {
				t.Error("access list is not protected from inherited entries")
			}
			dacl, _, err := descriptor.DACL()
			if err != nil {
				t.Fatal(err)
			}
			userGranted := false
			for index := range uint32(dacl.AceCount) {
				var ace *windows.ACCESS_ALLOWED_ACE
				if err := windows.GetAce(dacl, index, &ace); err != nil {
					t.Fatal(err)
				}
				sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
				if !slices.ContainsFunc(allowed, sid.Equals) {
					t.Errorf("access list admits %s", sid)
				}
				userGranted = userGranted || sid.Equals(user.User.Sid)
			}
			if !userGranted {
				t.Error("access list does not admit the current user")
			}
		})
	}
}
