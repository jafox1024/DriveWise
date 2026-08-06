package analyzer

import (
	"testing"

	"drivewise/backend/models"
)

// TestMFTInvalidatePath 验证删除后 MFT 缓存树正确移除节点并扣减祖先大小
func TestMFTInvalidatePath(t *testing.T) {
	// 构造合成树：
	// C:\ (root, size=100)
	//   └─ A (dir, size=100)
	//        ├─ B (file, size=40)
	//        └─ C (file, size=60)
	root := &models.FileNode{Name: "C:\\", Path: `C:\`, IsDir: true, Size: 100}
	a := &models.FileNode{Name: "A", Path: `C:\A`, IsDir: true, Size: 100}
	b := &models.FileNode{Name: "B", Path: `C:\A\B`, Size: 40}
	c := &models.FileNode{Name: "C", Path: `C:\A\C`, Size: 60}
	a.Children = []*models.FileNode{b, c}
	root.Children = []*models.FileNode{a}

	mftCacheMu.Lock()
	mftCache["C"] = root
	mftCacheMu.Unlock()

	mftInvalidatePath(`C:\A\B`)

	if len(a.Children) != 1 || a.Children[0] != c {
		t.Fatalf("A 的子级应为 [C]，实际 %d 个", len(a.Children))
	}
	if a.Size != 60 {
		t.Errorf("A.Size = %d, want 60", a.Size)
	}
	if root.Size != 60 {
		t.Errorf("root.Size = %d, want 60", root.Size)
	}

	// 再次删除 C，A 应无子级，大小归零
	mftInvalidatePath(`C:\A\C`)
	if len(a.Children) != 0 {
		t.Errorf("A 的子级应为空，实际 %d 个", len(a.Children))
	}
	if a.Size != 0 {
		t.Errorf("A.Size = %d, want 0", a.Size)
	}
	if root.Size != 0 {
		t.Errorf("root.Size = %d, want 0", root.Size)
	}

	mftCacheMu.Lock()
	delete(mftCache, "C")
	mftCacheMu.Unlock()
}

// TestMFTInvalidatePathMissing 删除不存在的路径不应破坏树
func TestMFTInvalidatePathMissing(t *testing.T) {
	root := &models.FileNode{Name: "D:\\", Path: `D:\`, IsDir: true, Size: 10}
	child := &models.FileNode{Name: "x", Path: `D:\x`, Size: 10}
	root.Children = []*models.FileNode{child}

	mftCacheMu.Lock()
	mftCache["D"] = root
	mftCacheMu.Unlock()

	mftInvalidatePath(`D:\nope`)

	if len(root.Children) != 1 || root.Size != 10 {
		t.Errorf("树不应变化: children=%d size=%d", len(root.Children), root.Size)
	}

	mftCacheMu.Lock()
	delete(mftCache, "D")
	mftCacheMu.Unlock()
}
