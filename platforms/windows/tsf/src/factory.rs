//! IClassFactory：建立 TextService。
use std::ffi::c_void;

use windows::Win32::Foundation::{CLASS_E_NOAGGREGATION, E_POINTER};
use windows::Win32::System::Com::{IClassFactory, IClassFactory_Impl};
use windows_core::{implement, ComObject, IUnknown, Interface, Ref, BOOL, GUID};

use crate::service::TextService;

#[implement(IClassFactory)]
pub struct ClassFactory;

impl ClassFactory {
    pub fn new() -> ClassFactory {
        crate::object_created();
        ClassFactory
    }
}

impl Drop for ClassFactory {
    fn drop(&mut self) {
        crate::object_destroyed();
    }
}

impl IClassFactory_Impl for ClassFactory_Impl {
    fn CreateInstance(&self, outer: Ref<'_, IUnknown>, riid: *const GUID, ppv: *mut *mut c_void) -> windows_core::Result<()> {
        crate::guarded("CreateInstance", || {
            if ppv.is_null() || riid.is_null() {
                return Err(E_POINTER.into());
            }
            unsafe { ppv.write(std::ptr::null_mut()) };
            if outer.as_ref().is_some() {
                return Err(CLASS_E_NOAGGREGATION.into());
            }
            let unknown: IUnknown = ComObject::new(TextService::new()).into_interface();
            unsafe { unknown.query(riid, ppv).ok() }
        })
    }

    fn LockServer(&self, lock: BOOL) -> windows_core::Result<()> {
        crate::guarded("LockServer", || {
            if lock.as_bool() {
                crate::object_created();
            } else {
                crate::object_destroyed();
            }
            Ok(())
        })
    }
}
