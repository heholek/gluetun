// Code generated by MockGen. DO NOT EDIT.
// Source: github.com/qdm12/golibs/command (interfaces: Commander)

// Package routing is a generated GoMock package.
package routing

import (
	gomock "github.com/golang/mock/gomock"
	io "io"
	reflect "reflect"
)

// MockCommander is a mock of Commander interface
type MockCommander struct {
	ctrl     *gomock.Controller
	recorder *MockCommanderMockRecorder
}

// MockCommanderMockRecorder is the mock recorder for MockCommander
type MockCommanderMockRecorder struct {
	mock *MockCommander
}

// NewMockCommander creates a new mock instance
func NewMockCommander(ctrl *gomock.Controller) *MockCommander {
	mock := &MockCommander{ctrl: ctrl}
	mock.recorder = &MockCommanderMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use
func (m *MockCommander) EXPECT() *MockCommanderMockRecorder {
	return m.recorder
}

// Run mocks base method
func (m *MockCommander) Run(arg0 string, arg1 ...string) (string, error) {
	m.ctrl.T.Helper()
	varargs := []interface{}{arg0}
	for _, a := range arg1 {
		varargs = append(varargs, a)
	}
	ret := m.ctrl.Call(m, "Run", varargs...)
	ret0, _ := ret[0].(string)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// Run indicates an expected call of Run
func (mr *MockCommanderMockRecorder) Run(arg0 interface{}, arg1 ...interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	varargs := append([]interface{}{arg0}, arg1...)
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Run", reflect.TypeOf((*MockCommander)(nil).Run), varargs...)
}

// Start mocks base method
func (m *MockCommander) Start(arg0 string, arg1 ...string) (io.ReadCloser, io.ReadCloser, func() error, error) {
	m.ctrl.T.Helper()
	varargs := []interface{}{arg0}
	for _, a := range arg1 {
		varargs = append(varargs, a)
	}
	ret := m.ctrl.Call(m, "Start", varargs...)
	ret0, _ := ret[0].(io.ReadCloser)
	ret1, _ := ret[1].(io.ReadCloser)
	ret2, _ := ret[2].(func() error)
	ret3, _ := ret[3].(error)
	return ret0, ret1, ret2, ret3
}

// Start indicates an expected call of Start
func (mr *MockCommanderMockRecorder) Start(arg0 interface{}, arg1 ...interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	varargs := append([]interface{}{arg0}, arg1...)
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Start", reflect.TypeOf((*MockCommander)(nil).Start), varargs...)
}