package baidupcs

import (
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
	"unsafe"
)

// Rename 重命名文件/目录
func (pcs *BaiduPCS) Rename(from, to string) (pcsError pcserror.Error) {
	return pcs.cpmvOp(OperationRename, &CpMvJSON{
		From: from,
		To:   to,
	})
}

// Copy 批量拷贝文件/目录
func (pcs *BaiduPCS) Copy(cpmvJSON ...*CpMvJSON) (pcsError pcserror.Error) {
	return pcs.cpmvOp(OperationCopy, cpmvJSON...)
}

// Move 批量移动文件/目录
func (pcs *BaiduPCS) Move(cpmvJSON ...*CpMvJSON) (pcsError pcserror.Error) {
	return pcs.cpmvOp(OperationMove, cpmvJSON...)
}

func (pcs *BaiduPCS) cpmvOp(op string, cpmvJSON ...*CpMvJSON) (pcsError pcserror.Error) {
	return pcs.cpmvPolicy(op, "fail", cpmvJSON...)
}
func (pcs *BaiduPCS) cpmvPolicy(op, policy string, cpmvJSON ...*CpMvJSON) (pcsError pcserror.Error) {
	dataReadCloser, err := pcs.prepareCpMvPolicy(op, policy, cpmvJSON...)
	if err != nil {
		return err
	}

	defer dataReadCloser.Close()

	errInfo := pcserror.DecodePCSJSONError(op, dataReadCloser)
	if errInfo != nil {
		return errInfo
	}

	// 更新缓存
	pcs.deleteCache((*CpMvJSONList)(unsafe.Pointer(&cpmvJSON)).AllRelatedDir())
	return nil
}

// CopyMovePolicy keeps the existing PCS parser/cache invalidation while selecting
// the upstream's explicit conflict policy. skip is resolved by the caller.
func (pcs *BaiduPCS) CopyMovePolicy(op, policy, from, to string) pcserror.Error {
	return pcs.cpmvPolicy(op, policy, &CpMvJSON{From: from, To: to})
}
