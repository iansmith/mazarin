target remote localhost:1234
set pagination off
monitor stop
info symbol $pc
x/20i $pc-40
x/20x $sp-64
print/x $sp
print/x $x28
print/x $x29
print/x $x30
info symbol $x30
x/10i $x30-20
