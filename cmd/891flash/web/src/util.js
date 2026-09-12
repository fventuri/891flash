// util.js — small shared helpers.

export const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

export const hex = (b) => b.toString(16).padStart(2, '0').toUpperCase();

export const hexSpace = (arr) => Array.from(arr, hex).join(' ');
